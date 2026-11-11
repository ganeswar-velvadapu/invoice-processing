package extract

import "time"

// NewEngine builds the OCR engine from settings (env names in parentheses):
//
//	engine (OCR_ENGINE)            "paddle" (PaddleOCR with Tesseract fallback) or "tesseract"
//	pythonBin (PADDLE_PYTHON)      interpreter with paddleocr installed
//	script (PADDLE_OCR_SCRIPT)     path to paddle_ocr_server.py
//	model (PADDLE_OCR_MODEL)       "mobile" (default, ~3s/page) or "server" (~7x slower, marginally better)
func NewEngine(engine, tesseractBin, lang, pythonBin, script, model string, onFallback func(error)) OCREngine {
	tess := &Tesseract{Bin: tesseractBin, Lang: lang}
	if engine != "paddle" {
		return tess
	}
	p := &Paddle{Python: pythonBin, Script: script, StartupWait: 3 * time.Minute}
	if model != "" {
		p.Env = append(p.Env, "PADDLE_OCR_MODEL="+model)
	}
	return &Fallback{Primary: p, Secondary: tess, OnFallback: onFallback}
}
