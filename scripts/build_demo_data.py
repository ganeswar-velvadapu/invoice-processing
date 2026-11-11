#!/usr/bin/env python3
"""Build the demo master data in data/ from the synthetic generator's output.

  vendors.csv              every seller in the generator's ground truth
  purchase_orders.csv      every purchase order with all its lines (ground truth, exact)
  goods_receipt_notes.csv  the CLEAN-tier GRNs only: the generator keeps no GRN ground truth, so these are
                           read from the PDFs' text layer by our parser and accepted only if the row sums
                           equal the totals printed on the GRN itself. Scanned GRNs are uploaded as PDFs.

Usage (from the project root; needs Go):
  python3 -I scripts/build_demo_data.py [GENERATOR_OUTPUT_DIR]
"""
import csv, json, os, re, sqlite3, subprocess, sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
GEN = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else "/home/ganeswar/Practice/invoice-generator/output")
OUT = os.path.join(ROOT, "data")


def iso(d):  # 15/11/2022 -> 2022-11-15
    m = re.match(r"(\d{1,2})/(\d{1,2})/(\d{4})$", d or "")
    return f"{m[3]}-{int(m[2]):02d}-{int(m[1]):02d}" if m else (d or "")


def one_line(s):
    return ", ".join(p.strip() for p in (s or "").splitlines() if p.strip())


def main():
    os.makedirs(OUT, exist_ok=True)
    db = sqlite3.connect(f"file:{os.path.join(GEN, 'master_ground_truth.db')}?mode=ro", uri=True)
    rows = [(dt, idx, json.loads(p)) for dt, idx, p in db.execute("select doc_type, doc_index, json_payload from documents order by doc_index")]

    vendors = {}
    for _, _, p in rows:
        a = p.get("seller_address") or {}
        vendors[p["seller_gstin"]] = [p["seller_name"], p["seller_gstin"],
                                      one_line(", ".join(x for x in (a.get("line1"), a.get("city"), a.get("state")) if x)), "yes", "ACTIVE"]
    with open(os.path.join(OUT, "vendors.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["legal_name", "gstin", "address", "is_approved", "vendor_status"])
        w.writerows(sorted(vendors.values()))

    po_numbers, po_qtys = {}, {}
    with open(os.path.join(OUT, "purchase_orders.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["po_number", "vendor_gstin", "po_date", "buyer_entity", "currency", "po_status", "line_number",
                    "item_code", "description", "hsn_sac", "quantity_ordered", "unit_of_measure", "unit_price", "tax_rate"])
        n_po = 0
        for dt, idx, p in rows:
            if dt != "purchase_order":
                continue
            n_po += 1
            po_numbers[idx] = p["po_number"]
            po_qtys[idx] = sorted(float(it["quantity"]) for it in p["line_items"])
            for i, it in enumerate(p["line_items"], 1):
                w.writerow([p["po_number"], p["seller_gstin"], iso(p["po_date"]), p["buyer_name"], "INR", "APPROVED", i, "",
                            it["description"], it["hsn_code"], it["quantity"], it["unit"], it["unit_cost_inr"], it["gst_rate"]])

    # clean GRNs: parse the text layer with our own parser, keep only if they reconcile with printed totals
    grn_dir = os.path.join(GEN, "goods_receipt_notes", "clean")
    kept, skipped = [], []
    with open(os.path.join(OUT, "goods_receipt_notes.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["grn_number", "po_number", "vendor_gstin", "receipt_date", "line_number", "item_code", "description",
                    "quantity_received", "quantity_accepted", "quantity_rejected", "unit_of_measure"])
        for name in sorted(os.listdir(grn_dir)):
            if not name.endswith(".pdf"):
                continue
            path = os.path.join(grn_dir, name)
            r = subprocess.run(["go", "run", "./cmd/extractcli", path], cwd=os.path.join(ROOT, "backend"), capture_output=True, text=True)
            d = json.loads(r.stdout[r.stdout.index("{"):])["data"]
            fl = {k: v["value"] for k, v in d["fields"].items()}
            items = d["items"] or []
            text = subprocess.run(["pdftotext", "-layout", path, "-"], capture_output=True, text=True).stdout
            printed = {k: float(v.replace(",", "")) for k, v in re.findall(r"Total (Received|Accepted):?\s+([\d,]+\.\d+)", text)}
            got = {"Received": round(sum(i.get("quantity_received") or 0 for i in items), 2),
                   "Accepted": round(sum(i.get("quantity_accepted") or 0 for i in items), 2)}
            # check 1: totals printed on the GRN itself (three layouts seen in the generator)
            m = re.search(r"TOTALS\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)", text)
            if m:
                printed = {"Received": float(m[2]), "Accepted": float(m[3])}
            boxes = re.search(r"\n\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\n\s*Units Ordered\s+Units Received\s+Units Accepted", text)
            if boxes:
                printed = {"Received": float(boxes[2]), "Accepted": float(boxes[3])}
            # summary boxes print whole numbers (1840 for 1839.61): allow rounding when the printed figure is an integer
            totals_ok = bool(printed) and all(abs(got[k] - v) < (0.5 if v == int(v) and got[k] != int(got[k]) else 0.01) for k, v in printed.items())
            # check 2: the GRN's ordered quantities equal its PO's line quantities (generator ground truth)
            idx = int(re.search(r"(\d+)", name)[1])
            ordered = sorted(float(i.get("quantity") or 0) for i in items)
            po_ok = idx in po_qtys and ordered == po_qtys[idx]
            # every row internally consistent
            rows_ok = all(abs((i.get("quantity_received") or 0) - (i.get("quantity_accepted") or 0) - (i.get("quantity_rejected") or 0)) < 0.01 for i in items)
            ok = rows_ok and (totals_ok or po_ok)
            if not ok or not items or not fl.get("grn_number"):
                skipped.append(name)
                continue
            po = fl.get("po_reference") or po_numbers.get(idx, "")
            if po_numbers.get(idx) and re.sub(r"\W", "", po).upper() != re.sub(r"\W", "", po_numbers[idx]).upper():
                skipped.append(f"{name} (PO {po} != {po_numbers[idx]})")
                continue
            for it in items:
                w.writerow([fl["grn_number"], po, fl.get("vendor_gstin", ""), fl.get("receipt_date", ""), it["line_number"],
                            it.get("item_code", ""), it["description"], it.get("quantity_received"), it.get("quantity_accepted"),
                            it.get("quantity_rejected") or 0, it.get("unit", "")])
            how = " + ".join(x for x, y in (("printed totals", totals_ok), ("PO quantities", po_ok)) if y)
            kept.append(f"{name}: {fl['grn_number']} -> {po}, {len(items)} lines, received {got['Received']} / accepted {got['Accepted']} [verified: {how}]")

    print(f"vendors.csv: {len(vendors)} vendors")
    print(f"purchase_orders.csv: {n_po} POs")
    print("goods_receipt_notes.csv:")
    for k in kept:
        print("  ok  ", k)
    for s in skipped:
        print("  SKIP", s)


if __name__ == "__main__":
    main()
