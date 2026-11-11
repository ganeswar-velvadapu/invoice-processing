package extract

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Paddle runs PaddleOCR (PP-OCRv5) through a long-lived Python helper (ocr/paddle_ocr_server.py),
// so models are loaded once. Requests are serialised; the helper is restarted if it dies or times out.
type Paddle struct {
	Python      string        // python interpreter with paddleocr installed
	Script      string        // path to paddle_ocr_server.py
	Env         []string      // extra environment (PADDLE_OCR_MODEL, ...)
	StartupWait time.Duration // model load timeout (default 3m)

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	nextID int
}

type paddleLine struct {
	Text string  `json:"text"`
	Conf float64 `json:"conf"`
	Box  [4]int  `json:"box"`
}

type paddleResp struct {
	ID    int          `json:"id"`
	Lines []paddleLine `json:"lines"`
	Error string       `json:"error"`
	Ready bool         `json:"ready"`
}

func (p *Paddle) start() error {
	cmd := exec.Command(p.Python, p.Script)
	cmd.Env = append(os.Environ(), p.Env...)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start paddleocr helper: %w", err)
	}
	p.cmd, p.stdin, p.out = cmd, stdin, bufio.NewReaderSize(stdout, 1<<20)
	wait := p.StartupWait
	if wait == 0 {
		wait = 3 * time.Minute
	}
	var r paddleResp
	if err := p.readLine(wait, &r); err != nil || !r.Ready {
		p.stop()
		if err == nil {
			err = errors.New("unexpected first line")
		}
		return fmt.Errorf("paddleocr helper did not become ready: %w", err)
	}
	return nil
}

func (p *Paddle) stop() {
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
		p.cmd.Wait()
	}
	p.cmd, p.stdin, p.out = nil, nil, nil
}

// readLine reads one JSON line with a timeout; on timeout the helper is killed so the read unblocks.
func (p *Paddle) readLine(timeout time.Duration, v any) error {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	out := p.out
	go func() {
		b, err := out.ReadBytes('\n')
		ch <- res{b, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		return json.Unmarshal(r.b, v)
	case <-time.After(timeout):
		p.stop()
		return errors.New("paddleocr helper timed out")
	}
}

func (p *Paddle) RecognizeImage(ctx context.Context, imagePath string, page int) ([]Line, float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil {
		if err := p.start(); err != nil {
			return nil, 0, err
		}
	}
	p.nextID++
	req, _ := json.Marshal(map[string]any{"id": p.nextID, "image": imagePath})
	if _, err := p.stdin.Write(append(req, '\n')); err != nil {
		p.stop()
		return nil, 0, fmt.Errorf("paddleocr helper: %w", err)
	}
	timeout := 5 * time.Minute
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}
	var r paddleResp
	if err := p.readLine(timeout, &r); err != nil {
		p.stop()
		return nil, 0, fmt.Errorf("paddleocr helper: %w", err)
	}
	if r.Error != "" {
		return nil, 0, errors.New("paddleocr: " + r.Error)
	}
	ws := make([]word, 0, len(r.Lines))
	for _, l := range r.Lines {
		ws = append(ws, word{text: l.Text, left: l.Box[0], top: l.Box[1], width: l.Box[2] - l.Box[0],
			height: l.Box[3] - l.Box[1], conf: l.Conf * 100})
	}
	lines, mean := assemble(ws, page)
	return lines, mean, nil
}

// Close stops the helper process.
func (p *Paddle) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stop()
}

// Fallback tries the primary engine and uses the secondary if the primary fails (not if it is merely unsure).
type Fallback struct {
	Primary, Secondary OCREngine
	OnFallback         func(err error)
}

func (f *Fallback) RecognizeImage(ctx context.Context, imagePath string, page int) ([]Line, float64, error) {
	lines, conf, err := f.Primary.RecognizeImage(ctx, imagePath, page)
	if err == nil {
		return lines, conf, nil
	}
	if f.OnFallback != nil {
		f.OnFallback(err)
	}
	return f.Secondary.RecognizeImage(ctx, imagePath, page)
}
