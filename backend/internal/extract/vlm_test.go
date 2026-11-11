package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// A fake Ollama: checks the request shape and returns one page of JSON per call.
func TestVLMClientMergesPages(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed")
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Model    string `json:"model"`
			Format   any    `json:"format"`
			Messages []struct {
				Content string   `json:"content"`
				Images  []string `json:"images"`
			} `json:"messages"`
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &req); err != nil || req.Model != "test-model" || req.Format == nil ||
			len(req.Messages) != 1 || len(req.Messages[0].Images) != 1 || !strings.Contains(req.Messages[0].Content, "Only transcribe") {
			http.Error(w, "bad request shape", 400)
			return
		}
		page := `{"document_type":"INVOICE","document_number":"INV-1","seller":{"gstin":"27AAPFU0939F1ZV"},
			"items":[{"description":"Bearing","quantity":2,"unit_price":5,"amount":10}],"grand_total":null}`
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": page}})
	}))
	defer srv.Close()
	v := &VLM{URL: srv.URL, Model: "test-model", PdftoppmBin: "pdftoppm", MaxPx: 400}
	doc, err := v.Extract(context.Background(), "../../testdata/inv_INV-2026-0042_acme_qty_exceeds_grn.pdf", "application/pdf", "INVOICE")
	if err != nil {
		t.Fatal(err)
	}
	if calls != doc.Pages || doc.Pages < 1 {
		t.Fatalf("one request per page expected: calls=%d pages=%d", calls, doc.Pages)
	}
	if doc.DocumentType != "INVOICE" || doc.DocumentNumber == nil || *doc.DocumentNumber != "INV-1" || len(doc.Items) != doc.Pages {
		t.Fatalf("merge failed: %+v", doc)
	}
	if doc.FieldPage["document_number"] != 1 || doc.Items[0].Page != 1 {
		t.Fatalf("page tracking failed: %+v", doc.FieldPage)
	}
}

func TestVLMUnreachable(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed")
	}
	v := &VLM{URL: "http://127.0.0.1:1", Model: "m", PdftoppmBin: "pdftoppm", MaxPx: 300}
	_, err := v.Extract(context.Background(), "../../testdata/po_PO-1001_acme.pdf", "application/pdf", "")
	if err == nil || !strings.Contains(err.Error(), "ollama unreachable") {
		t.Fatalf("expected a clear unreachable error, got %v", err)
	}
}
