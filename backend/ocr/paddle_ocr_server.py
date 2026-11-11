#!/usr/bin/env python3
"""Long-lived PaddleOCR helper for the Go worker.

Protocol (one JSON object per line):
  stdin : {"id": 1, "image": "/path/page.png"}
  stdout: {"id": 1, "lines": [{"text": "...", "conf": 0.98, "box": [x0, y0, x1, y1]}, ...]}
       or {"id": 1, "error": "..."}
A {"ready": true, ...} line is printed once the models are loaded.

Models are loaded once per process (loading takes seconds; recognising a page much less).
Configure with PADDLE_OCR_MODEL=mobile|server (default mobile: ~3s/page vs ~22s on CPU, similar accuracy) and PADDLE_OCR_LANG (default en).
"""
import json
import os
import sys
import time

os.environ.setdefault("PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK", "True")
os.environ.setdefault("FLAGS_logtostderr", "0")


def log(msg):
    print(msg, file=sys.stderr, flush=True)


def main():
    # stdout is the protocol channel: keep library chatter on stderr
    real_stdout = sys.stdout
    sys.stdout = sys.stderr
    from paddleocr import PaddleOCR

    variant = os.environ.get("PADDLE_OCR_MODEL", "mobile")
    lang = os.environ.get("PADDLE_OCR_LANG", "en")
    kw = dict(use_doc_orientation_classify=False, use_doc_unwarping=False, use_textline_orientation=False,
              enable_mkldnn=os.environ.get("PADDLE_OCR_MKLDNN", "1") == "1")
    if lang == "en":
        kw.update(text_detection_model_name=f"PP-OCRv5_{variant}_det", text_recognition_model_name=f"PP-OCRv5_{variant}_rec")
    else:
        kw.update(lang=lang)
    t0 = time.time()
    ocr = PaddleOCR(**kw)
    out = real_stdout
    out.write(json.dumps({"ready": True, "model": variant, "load_seconds": round(time.time() - t0, 1)}) + "\n")
    out.flush()

    for raw in sys.stdin:
        raw = raw.strip()
        if not raw:
            continue
        rid = None
        try:
            req = json.loads(raw)
            rid = req.get("id")
            res = ocr.predict(req["image"])[0]
            lines = []
            for text, score, box in zip(res["rec_texts"], res["rec_scores"], res["rec_boxes"]):
                if not text.strip():
                    continue
                x0, y0, x1, y1 = (int(v) for v in box)
                lines.append({"text": text, "conf": round(float(score), 4), "box": [x0, y0, x1, y1]})
            resp = {"id": rid, "lines": lines}
        except Exception as e:  # report and keep serving
            resp = {"id": rid, "error": f"{type(e).__name__}: {e}"}
        out.write(json.dumps(resp, ensure_ascii=False) + "\n")
        out.flush()


if __name__ == "__main__":
    main()
