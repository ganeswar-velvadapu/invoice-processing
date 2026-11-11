#!/usr/bin/env python3
"""End-to-end smoke test against a running backend using the hand-made fixtures in backend/testdata.

Scenario (documents arrive in any order):
  1. import vendors + PO-1002/GRN-5002 (Bharat) as master data
  2. upload Acme invoice INV-2026-0042 before its PO exists      -> PENDING_PO_MATCH
  3. upload PO-1001                                              -> invoice waits for GRN (PENDING_GRN)
  4. upload GRN-5001 (90 of 100 bearings accepted)               -> HOLD, QUANTITY_EXCEEDS_GRN
  5. upload INV-2026-0043 (90 bearings)                          -> HOLD (cumulative 100+90 > 90 accepted)
  6. reject INV-0042                                             -> INV-0043 re-verified -> APPROVE
  7. Bharat invoice with a price above PO                        -> HOLD, PRICE_MISMATCH
  8. re-upload INV-0043 file                                     -> HOLD, DUPLICATE_INVOICE
Usage: python3 scripts/e2e_smoke.py [base_url]  (env ADMIN_EMAIL / ADMIN_PASSWORD)
"""
import json, os, sys, time, uuid, urllib.request, urllib.error

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080").rstrip("/") + "/api"
SAMPLES = os.path.join(os.path.dirname(__file__), "..", "backend", "testdata")
TOKEN = None
failures = []


def req(method, path, body=None, files=None, fields=None, expect=None):
    headers = {}
    data = None
    if TOKEN:
        headers["Authorization"] = "Bearer " + TOKEN
    if files:
        b = uuid.uuid4().hex
        parts = []
        for k, v in (fields or {}).items():
            parts.append(f'--{b}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode())
        for k, p in files.items():
            parts.append(f'--{b}\r\nContent-Disposition: form-data; name="{k}"; filename="{os.path.basename(p)}"\r\n'
                         f'Content-Type: application/octet-stream\r\n\r\n'.encode() + open(p, "rb").read() + b"\r\n")
        parts.append(f"--{b}--\r\n".encode())
        data = b"".join(parts)
        headers["Content-Type"] = f"multipart/form-data; boundary={b}"
    elif body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    r = urllib.request.Request(BASE + path, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(r) as resp:
            out = json.loads(resp.read() or b"null")
            code = resp.status
    except urllib.error.HTTPError as e:
        code, out = e.code, json.loads(e.read() or b"null")
    if expect and code != expect:
        raise SystemExit(f"{method} {path} -> {code} {out}")
    return code, out


def wait_idle(timeout=90):
    t = time.time()
    while time.time() - t < timeout:
        _, s = req("GET", "/dashboard/summary")
        if not s["jobs"].get("QUEUED") and not s["jobs"].get("RUNNING"):
            return s
        time.sleep(0.5)
    raise SystemExit("worker did not finish in time")


def upload(name, doc_type=""):
    _, r = req("POST", "/documents", files={"file": os.path.join(SAMPLES, name)}, fields={"document_type": doc_type}, expect=201)
    wait_idle()
    return r["document_id"]


def invoice_by_number(num):
    _, r = req("GET", f"/invoices?q={num}&limit=10")
    items = [i for i in r["items"] if i["invoice_number"] == num]
    return sorted(items, key=lambda i: i["created_at"])


def expect(label, inv, status, decision, exc=None):
    _, full = req("GET", f"/invoices/{inv['invoice_id']}")
    _, excs = req("GET", f"/invoices/{inv['invoice_id']}/exceptions")
    open_codes = sorted({e["exception_code"] for e in excs if e["exception_status"] in ("OPEN", "ASSIGNED")})
    ok = full["invoice_status"] == status and full["decision"] == decision and (exc is None or exc in open_codes)
    mark = "PASS" if ok else "FAIL"
    print(f"[{mark}] {label}: status={full['invoice_status']} decision={full['decision']} open={open_codes}\n       {full['decision_summary']}")
    if not ok:
        failures.append(label)
    return full


def main():
    global TOKEN
    _, r = req("POST", "/auth/login", {"email": os.environ.get("ADMIN_EMAIL", "admin@example.com"),
                                       "password": os.environ.get("ADMIN_PASSWORD", "Admin12345")}, expect=200)
    TOKEN = r["token"]
    imp = json.load(open(os.path.join(SAMPLES, "procurement_import.json")))
    _, r = req("POST", "/imports/procurement", imp, expect=200)
    print("import:", r)

    upload("inv_INV-2026-0042_acme_qty_exceeds_grn.pdf")
    inv42 = invoice_by_number("INV-2026-0042")[0]
    expect("INV-0042 before PO", inv42, "PENDING_PO_MATCH", "PENDING_DOCUMENTS", "PO_NOT_FOUND")

    upload("po_PO-1001_acme.pdf", "PO")
    wait_idle()
    expect("INV-0042 after PO, before GRN", inv42, "PENDING_GRN", "PENDING_DOCUMENTS", "GRN_NOT_RECEIVED")

    upload("grn_GRN-5001_acme.pdf")
    wait_idle()
    expect("INV-0042 after GRN (100 billed, 90 accepted)", inv42, "ON_HOLD", "HOLD", "QUANTITY_EXCEEDS_GRN")
    _, d = req("GET", f"/invoices/{inv42['invoice_id']}")
    print("       decision detail:", json.dumps({k: d["decision_detail"][k] for k in ("decision", "summary", "recommended_action")}))

    upload("inv_INV-2026-0043_acme_clean.pdf")
    inv43 = invoice_by_number("INV-2026-0043")[0]
    expect("INV-0043 while INV-0042 still claims 100", inv43, "ON_HOLD", "HOLD", "QUANTITY_EXCEEDS_GRN")

    req("POST", f"/invoices/{inv42['invoice_id']}/reject", {"reason": "Billed 100 but only 90 accepted; supplier to reissue"}, expect=200)
    wait_idle()
    expect("INV-0043 after INV-0042 rejected", inv43, "READY_FOR_VERIFICATION", "APPROVE")
    req("POST", f"/invoices/{inv43['invoice_id']}/approve", {"note": "matches GRN"}, expect=200)

    upload("inv_BEC-778_bharat_price_mismatch.pdf")
    inv778 = invoice_by_number("BEC/26-27/778")[0]
    expect("BEC-778 price above PO", inv778, "ON_HOLD", "HOLD", "PRICE_MISMATCH")

    upload("inv_INV-2026-0043_acme_clean.pdf")
    dups = invoice_by_number("INV-2026-0043")
    expect("INV-0043 re-uploaded", dups[-1], "ON_HOLD", "HOLD", "DUPLICATE_INVOICE")

    # --- roles: return to clerk / resubmit, and PO uploads limited to managers
    admin_token = TOKEN
    req("POST", "/users", {"email": "clerk@example.test", "full_name": "Test Clerk", "password": "Clerk12345", "role": "AP_CLERK"}, expect=201)
    _, r = req("POST", "/auth/login", {"email": "clerk@example.test", "password": "Clerk12345"}, expect=200)
    TOKEN = r["token"]
    code, _ = req("POST", "/documents", files={"file": os.path.join(SAMPLES, "po_PO-1001_acme.pdf")}, fields={"document_type": "PO"})
    print(f"[{'PASS' if code == 403 else 'FAIL'}] clerk uploading a PO is refused: HTTP {code}")
    if code != 403:
        failures.append("clerk PO upload")
    code, _ = req("POST", f"/invoices/{inv778['invoice_id']}/return", {"note": "please check line 2 price"})
    print(f"[{'PASS' if code == 403 else 'FAIL'}] clerk cannot return invoices: HTTP {code}")
    TOKEN = admin_token
    req("POST", f"/invoices/{inv778['invoice_id']}/return", {"note": "Line 2 price is above the PO, confirm with the supplier"}, expect=200)
    req("POST", f"/invoices/{inv778['invoice_id']}/verify", expect=200)
    expect("BEC-778 returned, survives re-verification", inv778, "RETURNED", "HOLD", "PRICE_MISMATCH")
    TOKEN = r["token"]
    req("POST", f"/invoices/{inv778['invoice_id']}/resubmit", {"note": "Supplier confirmed the price; credit note to follow"}, expect=200)
    TOKEN = admin_token
    expect("BEC-778 after clerk resubmits", inv778, "ON_HOLD", "HOLD", "PRICE_MISMATCH")
    _, evs = req("GET", f"/invoices/{inv778['invoice_id']}/events", expect=200)
    kinds = [e["event_type"] for e in evs]
    ok = "RETURNED_TO_CLERK" in kinds and "RESUBMITTED" in kinds
    print(f"[{'PASS' if ok else 'FAIL'}] audit trail has RETURNED_TO_CLERK and RESUBMITTED")
    if not ok:
        failures.append("audit events")

    # --- clerk checks the exception against PO/GRN and sends it for approval; manager approves against the system
    clerk_token = r["token"]
    TOKEN = clerk_token
    code, _ = req("POST", f"/invoices/{inv778['invoice_id']}/submit", {"recommendation": "APPROVE", "note": "Looks fine"})
    print(f"[{'PASS' if code in (400, 409) else 'FAIL'}] recommend-approve refused while exceptions are open: HTTP {code}")
    if code not in (400, 409):
        failures.append("submit with open exceptions")
    _, excs = req("GET", f"/invoices/{inv778['invoice_id']}/exceptions", expect=200)
    for e in excs:
        if e["exception_status"] in ("OPEN", "ASSIGNED"):
            req("POST", f"/exceptions/{e['invoice_exception_id']}", {"action": "CHECK", "note": "Supplier confirmed the revised rate by email; PO amendment pending"}, expect=200)
    req("POST", f"/invoices/{inv778['invoice_id']}/submit", {"recommendation": "APPROVE", "note": "Price difference confirmed with buyer; OK to pay"}, expect=200)
    TOKEN = admin_token
    req("POST", f"/invoices/{inv778['invoice_id']}/verify", expect=200)
    full = expect("BEC-778 sent for approval, survives re-verification", inv778, "AWAITING_APPROVAL", "HOLD")
    ok = full.get("clerk_recommendation") == "APPROVE" and full.get("submitted_by") == "clerk@example.test"
    print(f"[{'PASS' if ok else 'FAIL'}] clerk recommendation and submitter are recorded")
    if not ok:
        failures.append("clerk recommendation")
    code, _ = req("POST", f"/invoices/{inv778['invoice_id']}/approve", {"note": ""})
    print(f"[{'PASS' if code == 400 else 'FAIL'}] approving against a HOLD without a reason is refused: HTTP {code}")
    if code != 400:
        failures.append("approve without reason")
    req("POST", f"/invoices/{inv778['invoice_id']}/approve", {"note": "Accepting clerk's check; PO amendment to follow"}, expect=200)
    _, full = req("GET", f"/invoices/{inv778['invoice_id']}")
    _, evs = req("GET", f"/invoices/{inv778['invoice_id']}/events", expect=200)
    kinds = [e["event_type"] for e in evs]
    ok = full["invoice_status"] == "APPROVED" and "SENT_FOR_APPROVAL" in kinds and "APPROVED_AGAINST_SYSTEM" in kinds
    print(f"[{'PASS' if ok else 'FAIL'}] BEC-778 approved against system with audit trail: status={full['invoice_status']}")
    if not ok:
        failures.append("approve against system")

    _, cmp_ = req("GET", f"/invoices/{inv43['invoice_id']}/comparison", expect=200)
    pl = cmp_[0]["po_lines"][0]
    print(f"       comparison INV-0043 line1: qty {cmp_[0]['quantity']} vs ordered {pl['quantity_ordered']}, accepted {pl['quantity_accepted']}")
    _, summ = req("GET", "/dashboard/summary", expect=200)
    print("summary:", json.dumps(summ["invoices_by_status"]), json.dumps(summ["open_exceptions_by_code"]))
    if failures:
        raise SystemExit(f"{len(failures)} scenario(s) failed: {failures}")
    print("ALL SCENARIOS PASSED")


if __name__ == "__main__":
    main()
