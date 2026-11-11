#!/usr/bin/env python3
"""Offline extraction benchmark against the synthetic generator's ground truth.

The application never reads ground truth; this script only compares results produced by
`extractcli` (classification + extraction, no database) with it.

Usage:
  python3 -I scripts/benchmark_extraction.py GENERATOR_OUTPUT_DIR [--image invoice-processing-api] [--jobs 8]

GENERATOR_OUTPUT_DIR contains purchase_orders/, tax_invoices/, ... and master_ground_truth.db.
"""
import argparse, collections, concurrent.futures as cf, json, os, re, sqlite3, subprocess, sys

EXPECTED = {"purchase_orders": "PO", "tax_invoices": "INVOICE", "goods_receipt_notes": "GRN",
            "material_return_notes": "MRN", "waybills": "EWAY_BILL"}


def norm(s):
    return re.sub(r"[^A-Z0-9]", "", (s or "").upper())


def extract(root, rel, image, env):
    cmd = ["docker", "run", "--rm", "--user", "root", "-v", f"{root}:/data:ro,z", "-w", "/data"]
    for k, v in env.items():
        cmd += ["-e", f"{k}={v}"]
    cmd += ["--entrypoint", "extractcli", image, rel]
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=600)
    try:
        return json.loads(r.stdout[r.stdout.index("{"):])
    except ValueError:
        return {"error": (r.stderr or r.stdout)[-300:]}


def ground_truth(root):
    gt = {}
    db = os.path.join(root, "master_ground_truth.db")
    if not os.path.exists(db):
        return gt
    c = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    for doc_type, fn, payload in c.execute("select doc_type, filename, json_payload from documents"):
        p = json.loads(payload)
        stem = os.path.splitext(os.path.basename(fn))[0]
        gt[stem] = (doc_type, p)
    return gt


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("root")
    ap.add_argument("--image", default="invoice-processing-api")
    ap.add_argument("--jobs", type=int, default=8)
    ap.add_argument("--master", action="store_true",
                    help="pass the known vendor/buyer GSTINs, as the vendor master and BUYER_GSTINS would in production")
    a = ap.parse_args()
    root = os.path.abspath(a.root)
    files = sorted(os.path.relpath(os.path.join(d, f), root) for d, _, fs in os.walk(root) for f in fs if f.endswith(".pdf"))
    gt = ground_truth(root)
    env = {}
    if a.master:
        env["VENDOR_GSTINS"] = ",".join(sorted({p.get("seller_gstin") for _, p in gt.values() if p.get("seller_gstin")}))
        env["BUYER_GSTINS"] = ",".join(sorted({p.get("buyer_gstin") for _, p in gt.values() if p.get("buyer_gstin")}))
    with cf.ThreadPoolExecutor(a.jobs) as ex:
        results = dict(zip(files, ex.map(lambda f: extract(root, f, a.image, env), files)))

    cls_ok, cls_tot = collections.Counter(), collections.Counter()
    field_ok, field_tot = collections.Counter(), collections.Counter()
    wrong = []
    for rel, r in results.items():
        folder, tier = rel.split(os.sep)[0], rel.split(os.sep)[1]
        exp = EXPECTED.get(folder)
        got = r.get("classification", {}).get("document_type", "ERROR")
        key = (folder, tier)
        cls_tot[key] += 1
        if got == exp:
            cls_ok[key] += 1
        else:
            wrong.append(f"{rel}: {got} (ocr {round(r.get('ocr_conf', 0))})")
        stem = os.path.splitext(os.path.basename(rel))[0]
        if stem not in gt or "data" not in r:
            continue
        doc_type, p = gt[stem]
        f = {k: v["value"] for k, v in (r["data"].get("fields") or {}).items()}
        items = r["data"].get("items") or []
        checks = {}
        if doc_type == "purchase_order":
            checks = {"po_number": norm(f.get("po_number")) == norm(p.get("po_number")),
                      "vendor_gstin": f.get("vendor_gstin") == p.get("seller_gstin"),
                      "buyer_gstin": f.get("buyer_gstin") == p.get("buyer_gstin")}
        elif doc_type == "tax_invoice":
            gt_total = p.get("grand_total")
            try:
                total_ok = gt_total is not None and abs(float(f.get("grand_total", "nan")) - float(gt_total)) <= 1.0
            except ValueError:
                total_ok = False
            checks = {"invoice_number": norm(f.get("invoice_number")) == norm(p.get("invoice_number")),
                      "invoice_date": f.get("invoice_date", "") == "-".join(reversed((p.get("invoice_date") or "").split("/"))),
                      "po_reference": norm(p.get("po_number")) in [norm(x) for x in f.get("po_reference", "").split(",")],
                      "seller_gstin": f.get("seller_gstin") == p.get("seller_gstin"),
                      "buyer_gstin": f.get("buyer_gstin") == p.get("buyer_gstin"),
                      "grand_total": total_ok}
        n_gt = len(p.get("line_items") or [])
        checks["item_count"] = n_gt > 0 and len(items) == n_gt
        for k, ok in checks.items():
            field_tot[(doc_type, tier, k)] += 1
            field_ok[(doc_type, tier, k)] += bool(ok)

    print("== Classification (expected type from folder)")
    tot = sum(cls_tot.values())
    print(f"overall {sum(cls_ok.values())}/{tot}")
    for k in sorted(cls_tot):
        print(f"  {k[0]:22} {k[1]:11} {cls_ok[k]}/{cls_tot[k]}")
    if wrong:
        print("  misclassified:", *wrong, sep="\n    ")
    print("\n== Header fields vs ground truth (PO + invoice)")
    by_field = collections.defaultdict(lambda: [0, 0])
    for (dt, tier, k), n in field_tot.items():
        by_field[(dt, k)][0] += field_ok[(dt, tier, k)]
        by_field[(dt, k)][1] += n
    for (dt, k), (ok, n) in sorted(by_field.items()):
        print(f"  {dt:15} {k:15} {ok}/{n}")
    by_tier = collections.defaultdict(lambda: [0, 0])
    for (dt, tier, k), n in field_tot.items():
        by_tier[tier][0] += field_ok[(dt, tier, k)]
        by_tier[tier][1] += n
    print("  by tier: " + ", ".join(f"{t} {ok}/{n}" for t, (ok, n) in sorted(by_tier.items())))
    allok = sum(field_ok.values()); alln = sum(field_tot.values())
    print(f"  total field accuracy {allok}/{alln} = {allok / max(alln, 1):.0%}")


if __name__ == "__main__":
    sys.exit(main())
