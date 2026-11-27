package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 headers and names
	"github.com/emersion/go-message/mail"
)

// Email ingestion: the worker polls the AP mailbox over IMAP, and every PDF or image attachment of an unread
// message goes through Ingest exactly like a manual upload (hashing, duplicate detection, extraction, checks).
// Messages are recorded by Message-ID so a re-poll never imports the same email twice.

const maxEmailsPerPoll = 25

// RunEmailPoller polls the mailbox until ctx ends. It returns at once when IMAP is not configured.
func (s *Service) RunEmailPoller(ctx context.Context, database *sql.DB) {
	if !s.Cfg.IMAPEnabled() {
		return
	}
	slog.Info("email ingestion started", "mailbox", s.Cfg.IMAPUser, "host", s.Cfg.IMAPHost, "folder", s.Cfg.IMAPFolder, "every", s.Cfg.IMAPPoll)
	for {
		n, err := s.PollMailbox(ctx, database)
		if err != nil {
			slog.Error("email poll failed", "err", err)
		} else if n > 0 {
			slog.Info("email poll", "messages", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.Cfg.IMAPPoll):
		}
	}
}

// PollMailbox handles the unread messages once and returns how many were processed.
func (s *Service) PollMailbox(ctx context.Context, database *sql.DB) (int, error) {
	addr := net.JoinHostPort(s.Cfg.IMAPHost, strconv.Itoa(s.Cfg.IMAPPort))
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	var c *client.Client
	var err error
	if s.Cfg.IMAPTLS {
		c, err = client.DialWithDialerTLS(dialer, addr, &tls.Config{ServerName: s.Cfg.IMAPHost})
	} else {
		c, err = client.DialWithDialer(dialer, addr)
	}
	if err != nil {
		return 0, fmt.Errorf("connect %s: %w", addr, err)
	}
	defer c.Logout()
	c.Timeout = 2 * time.Minute
	if err := c.Login(s.Cfg.IMAPUser, s.Cfg.IMAPPassword); err != nil {
		return 0, fmt.Errorf("login as %s (for Gmail use an app password): %w", s.Cfg.IMAPUser, err)
	}
	if _, err := c.Select(s.Cfg.IMAPFolder, false); err != nil {
		return 0, fmt.Errorf("open folder %s: %w", s.Cfg.IMAPFolder, err)
	}
	crit := imap.NewSearchCriteria()
	crit.WithoutFlags = []string{imap.SeenFlag}
	uids, err := c.UidSearch(crit)
	if err != nil {
		return 0, fmt.Errorf("search unread: %w", err)
	}
	if len(uids) == 0 {
		return 0, nil
	}
	if len(uids) > maxEmailsPerPoll {
		uids = uids[:maxEmailsPerPoll]
	}
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	section := &imap.BodySectionName{Peek: true} // read without marking seen; we mark after storing
	msgs := make(chan *imap.Message, len(uids))
	if err := c.UidFetch(set, []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, section.FetchItem()}, msgs); err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	done := new(imap.SeqSet)
	n := 0
	for m := range msgs {
		if ctx.Err() != nil {
			break
		}
		body := m.GetBody(section)
		if body == nil {
			continue
		}
		if err := s.ingestEmail(ctx, database, m.Envelope, body, m.Uid); err != nil {
			slog.Error("email not ingested; will retry next poll", "uid", m.Uid, "err", err)
			continue
		}
		done.AddNum(m.Uid)
		n++
	}
	if !done.Empty() {
		if err := c.UidStore(done, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.SeenFlag}, nil); err != nil {
			return n, fmt.Errorf("mark read: %w", err)
		}
	}
	return n, nil
}

type emailAttachment struct {
	name string
	data []byte
}

// ingestEmail records one message and ingests its attachments. An error means nothing was recorded and the
// message stays unread for the next poll.
func (s *Service) ingestEmail(ctx context.Context, database *sql.DB, env *imap.Envelope, body io.Reader, uid uint32) error {
	mr, err := mail.CreateReader(body)
	if err != nil {
		return fmt.Errorf("parse message: %w", err)
	}
	msgID, fromAddr, fromName, subject := "", "", "", ""
	var sent *time.Time
	if env != nil {
		msgID = strings.Trim(env.MessageId, "<> ")
		subject = env.Subject
		if len(env.From) > 0 {
			fromAddr, fromName = env.From[0].Address(), env.From[0].PersonalName
		}
		if !env.Date.IsZero() {
			sent = &env.Date
		}
	}
	if msgID == "" {
		msgID = fmt.Sprintf("uid-%d@%s/%s", uid, s.Cfg.IMAPUser, s.Cfg.IMAPFolder)
	}
	var exists bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM email_messages WHERE message_id=$1)`, msgID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil // already imported (e.g. marked unread again in the mail client)
	}

	var atts []emailAttachment
	var skipped []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read part: %w", err)
		}
		name, ctype, attachment := partInfo(p.Header)
		isPDF := ctype == "application/pdf" || strings.HasSuffix(strings.ToLower(name), ".pdf")
		isImage := strings.HasPrefix(ctype, "image/")
		switch {
		case isPDF, isImage && attachment: // inline images are logos in signatures, not documents
		case attachment:
			skipped = append(skipped, name+" (not a PDF or image)")
			continue
		default:
			continue // message text
		}
		data, err := io.ReadAll(io.LimitReader(p.Body, s.Cfg.MaxUploadBytes+1))
		if err != nil {
			return fmt.Errorf("read attachment %s: %w", name, err)
		}
		if name == "" {
			name = fmt.Sprintf("attachment-%d%s", len(atts)+1, map[bool]string{true: ".pdf", false: ""}[isPDF])
		}
		atts = append(atts, emailAttachment{name, data})
	}

	var emailID string
	if err := database.QueryRowContext(ctx, `INSERT INTO email_messages (message_id, mailbox, from_address, from_name, subject, sent_at,
		attachments_found, status) VALUES ($1,$2,$3,$4,$5,$6,$7,'INGESTED') RETURNING email_message_id`,
		msgID, s.Cfg.IMAPUser, nullable(fromAddr), nullable(fromName), nullable(subject), sent, len(atts)+len(skipped)).Scan(&emailID); err != nil {
		return err
	}
	created := 0
	for i, a := range atts {
		res, err := s.Ingest(ctx, database, IngestRequest{Reader: bytes.NewReader(a.data), OriginalFilename: a.name, SourceType: "EMAIL",
			SourceReference: fmt.Sprintf("%s#%d", msgID, i+1), EmailMessageID: emailID})
		var ue *UserError
		if errors.As(err, &ue) {
			skipped = append(skipped, a.name+": "+ue.Msg)
			continue
		}
		if err != nil {
			skipped = append(skipped, a.name+": could not be stored")
			slog.Error("email attachment not stored", "file", a.name, "err", err)
			continue
		}
		created++
		s.event(ctx, database, "", res.DocumentID, "EMAIL_RECEIVED", fmt.Sprintf("Received by email from %s, subject %q", emailFrom(fromName, fromAddr), subject), "",
			map[string]any{"message_id": msgID, "from": fromAddr, "subject": subject})
	}
	status := "INGESTED"
	switch {
	case len(atts)+len(skipped) == 0:
		status = "NO_ATTACHMENTS"
	case created == 0:
		status = "FAILED"
	case len(skipped) > 0:
		status = "PARTIAL"
	}
	_, err = database.ExecContext(ctx, `UPDATE email_messages SET documents_created=$2, status=$3, note=$4 WHERE email_message_id=$1`,
		emailID, created, status, nullable(strings.Join(skipped, "; ")))
	slog.Info("email ingested", "from", fromAddr, "subject", subject, "documents", created, "status", status)
	return err
}

// partInfo returns the file name, media type and whether the part is a real attachment.
func partInfo(h mail.PartHeader) (name, ctype string, attachment bool) {
	switch hh := h.(type) {
	case *mail.AttachmentHeader:
		name, _ = hh.Filename()
		ctype, _, _ = hh.ContentType()
		return name, strings.ToLower(ctype), true
	case *mail.InlineHeader:
		ct, params, _ := hh.ContentType()
		name = params["name"]
		if name == "" {
			if _, dp, err := mime.ParseMediaType(hh.Get("Content-Disposition")); err == nil {
				name = dp["filename"]
			}
		}
		return name, strings.ToLower(ct), false
	}
	return "", "", false
}

func emailFrom(name, addr string) string {
	if name != "" && addr != "" {
		return name + " <" + addr + ">"
	}
	return name + addr
}

// EmailStatus summarises email ingestion for the UI.
type EmailStatus struct {
	Enabled  bool       `json:"enabled"`
	Mailbox  string     `json:"mailbox,omitempty"`
	Received int        `json:"received"`
	LastAt   *time.Time `json:"last_received_at,omitempty"`
}

func (s *Service) EmailStatus(ctx context.Context, database *sql.DB) (EmailStatus, error) {
	st := EmailStatus{Enabled: s.Cfg.IMAPEnabled()}
	if st.Enabled {
		st.Mailbox = s.Cfg.IMAPUser
	}
	var last sql.NullTime
	err := database.QueryRowContext(ctx, `SELECT count(*), max(received_at) FROM email_messages`).Scan(&st.Received, &last)
	if last.Valid {
		st.LastAt = &last.Time
	}
	return st, err
}
