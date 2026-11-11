#!/usr/bin/env python3
"""Send one document page to the local vision model (Ollama) and print the structured result.

Usage:
  python3 scripts/vlm_smoke.py FILE.pdf [--page 1] [--model qwen3-vl:4b-instruct] [--max-px 1600]

Prints the JSON the model returns and how long it took. Nothing leaves this machine:
Ollama runs locally at http://127.0.0.1:11434.
"""
import argparse, base64, json, os, subprocess, sys, tempfile, time, urllib.request

SCHEMA = {
    "type": "object",
    "properties": {
        "document_type": {"type": "string", "enum": ["INVOICE", "PO", "GRN", "EWAY_BILL", "MRN", "OTHER"]},
        "document_number": {"type": ["string", "null"]},
        "document_date": {"type": ["string", "null"], "description": "YYYY-MM-DD"},
        "po_reference": {"type": ["string", "null"]},
        "seller": {"type": "object", "properties": {"name": {"type": ["string", "null"]}, "gstin": {"type": ["string", "null"]}}},
        "buyer": {"type": "object", "properties": {"name": {"type": ["string", "null"]}, "gstin": {"type": ["string", "null"]}}},
        "items": {"type": "array", "items": {"type": "object", "properties": {
            "line": {"type": ["integer", "null"]}, "item_code": {"type": ["string", "null"]},
            "description": {"type": ["string", "null"]}, "hsn": {"type": ["string", "null"]},
            "unit": {"type": ["string", "null"]}, "quantity": {"type": ["number", "null"]},
            "unit_price": {"type": ["number", "null"]}, "amount": {"type": ["number", "null"]},
            "qty_received": {"type": ["number", "null"]}, "qty_accepted": {"type": ["number", "null"]},
            "qty_rejected": {"type": ["number", "null"]}}}},
        "total": {"type": ["number", "null"]},
    },
    "required": ["document_type", "document_number", "document_date", "po_reference", "seller", "buyer", "items", "total"],
}
for _p in ("seller", "buyer"):
    SCHEMA["properties"][_p]["required"] = ["name", "gstin"]
SCHEMA["properties"]["items"]["items"]["required"] = list(SCHEMA["properties"]["items"]["items"]["properties"])

PROMPT = """You are reading one page of an Indian business document (tax invoice, purchase order,
goods receipt note, e-way bill or material return note). Extract it into the JSON schema.
Rules:
- Copy values exactly as printed (document numbers, GSTINs, item codes). Do not correct or invent.
- Use null for anything not printed on this page.
- Dates as YYYY-MM-DD (printed dates are day-first, e.g. 23/11/2022 -> 2022-11-23).
- One entry in "items" per table row; join a description that wraps over several lines.
- For goods receipts fill qty_received / qty_accepted / qty_rejected; "quantity" is the ordered quantity.
- Numbers without currency symbols or thousands separators."""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("pdf")
    ap.add_argument("--page", type=int, default=1)
    ap.add_argument("--model", default=os.environ.get("VLM_MODEL", "qwen3-vl:4b-instruct"))
    ap.add_argument("--max-px", type=int, default=1600, help="longest side of the rendered page")
    ap.add_argument("--url", default="http://127.0.0.1:11434")
    a = ap.parse_args()

    with tempfile.TemporaryDirectory() as tmp:
        base = os.path.join(tmp, "page")
        subprocess.run(["pdftoppm", "-png", "-f", str(a.page), "-l", str(a.page), "-scale-to", str(a.max_px),
                        "-singlefile", a.pdf, base], check=True)
        img = base64.b64encode(open(base + ".png", "rb").read()).decode()

    body = {
        "model": a.model,
        "stream": False,
        "format": SCHEMA,
        "options": {"temperature": 0, "num_ctx": 16384, "num_predict": 4096},
        "messages": [{"role": "user", "content": PROMPT, "images": [img]}],
    }
    req = urllib.request.Request(a.url + "/api/chat", data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    print(f"model {a.model} | {os.path.basename(a.pdf)} page {a.page} | sending...", flush=True)
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=900) as r:
            resp = json.loads(r.read())
    except Exception as e:
        sys.exit(f"request failed: {e}\nIs Ollama running?  docker compose --profile vlm up -d ollama")
    took = time.time() - t0
    content = resp.get("message", {}).get("content", "")
    try:
        data = json.loads(content)
        print(json.dumps(data, indent=2, ensure_ascii=False))
        print(f"\n{len(data.get('items') or [])} item rows")
    except json.JSONDecodeError:
        print("model returned non-JSON output:\n" + content)
    print(f"took {took:.0f}s (prompt eval {resp.get('prompt_eval_duration', 0)/1e9:.0f}s, "
          f"generation {resp.get('eval_duration', 0)/1e9:.0f}s)")


if __name__ == "__main__":
    main()
