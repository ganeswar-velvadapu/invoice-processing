package extract

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHelper speaks the paddle_ocr_server.py protocol: two text lines per image,
// "crash" in the image path kills the process, "fail" returns an error.
const fakeHelper = `
import json, sys
print(json.dumps({"ready": True}), flush=True)
for raw in sys.stdin:
    req = json.loads(raw)
    if "crash" in req["image"]:
        sys.exit(1)
    if "fail" in req["image"]:
        print(json.dumps({"id": req["id"], "error": "bad image"}), flush=True); continue
    print(json.dumps({"id": req["id"], "lines": [
        {"text": "PURCHASE ORDER", "conf": 0.99, "box": [100, 50, 400, 80]},
        {"text": "PO-1001", "conf": 0.95, "box": [900, 52, 1050, 82]},
        {"text": "Qty", "conf": 0.97, "box": [100, 200, 160, 230]}]}), flush=True)
`

func TestPaddleProtocol(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	script := filepath.Join(t.TempDir(), "helper.py")
	os.WriteFile(script, []byte(fakeHelper), 0o644)
	p := &Paddle{Python: py, Script: script}
	defer p.Close()
	ctx := context.Background()

	lines, conf, err := p.RecognizeImage(ctx, "page.png", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.Contains(lines[0].Text, "PURCHASE ORDER") || !strings.Contains(lines[0].Text, "PO-1001") || lines[0].Page != 2 {
		t.Fatalf("unexpected lines: %+v", lines)
	}
	// same visual row is joined with a wide gap, like pdftotext -layout
	if !strings.Contains(lines[0].Text, "   ") {
		t.Errorf("columns should be separated by spaces: %q", lines[0].Text)
	}
	if conf < 95 || conf > 99 {
		t.Errorf("mean conf = %v", conf)
	}
	if _, _, err := p.RecognizeImage(ctx, "fail.png", 1); err == nil {
		t.Error("expected error for failing image")
	}
	if _, _, err := p.RecognizeImage(ctx, "crash.png", 1); err == nil {
		t.Error("expected error when helper dies")
	}
	// the helper is restarted transparently
	if _, _, err := p.RecognizeImage(ctx, "page.png", 1); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	// fallback engine uses tesseract-like secondary when primary errors
	fb := &Fallback{Primary: &Paddle{Python: py, Script: "/nonexistent.py"}, Secondary: p}
	if _, _, err := fb.RecognizeImage(ctx, "page.png", 1); err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
}
