-- One row per email seen in the invoice mailbox. message_id makes polling idempotent.
CREATE TABLE email_messages (
    email_message_id  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id        TEXT NOT NULL UNIQUE,
    mailbox           TEXT NOT NULL,
    from_address      TEXT,
    from_name         TEXT,
    subject           TEXT,
    sent_at           TIMESTAMPTZ,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    attachments_found INT NOT NULL DEFAULT 0,
    documents_created INT NOT NULL DEFAULT 0,
    status            TEXT NOT NULL CHECK (status IN ('INGESTED','NO_ATTACHMENTS','PARTIAL','FAILED')),
    note              TEXT
);
CREATE INDEX email_messages_received_idx ON email_messages (received_at DESC);
ALTER TABLE documents ADD COLUMN email_message_id UUID REFERENCES email_messages(email_message_id);
