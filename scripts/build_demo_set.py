#!/usr/bin/env python3
"""Build the demo dataset in data/demo/: master data (vendors, POs, GRNs as CSV for import) and
invoice PDFs in five different layouts, two of them handwritten bill-book scans (image-only PDFs).

Every invoice is planned against the master data so its expected decision is known (see
data/demo/README.md). Rendering uses Playwright's Chromium; handwritten pages use Google Fonts and are
rasterised with ImageMagick (rotation, blur, noise) and img2pdf so they have no text layer.

Usage: python3 scripts/build_demo_set.py [out_dir]
  (needs the playwright package with Chromium, ImageMagick and img2pdf)
"""
import csv, html, math, os, random, shutil, subprocess, sys, tempfile
from dataclasses import dataclass, field

OUT = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(__file__), "..", "data", "demo"))

# ---------- GSTIN checksum ----------
CH = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"


def gstin(base14):
    s = 0
    for i, c in enumerate(base14):
        p = CH.index(c) * (1 if i % 2 == 0 else 2)
        s += p // 36 + p % 36
    return base14 + CH[(36 - s % 36) % 36]


BUYER = {"name": "Inno Manufacturing Ltd", "gstin": gstin("29AAACI1234F1Z"),
         "addr": "Plot 41, Peenya Industrial Area, Bengaluru, Karnataka 560058"}


@dataclass
class Vendor:
    key: str
    name: str
    gstin: str
    addr: str
    phone: str
    approved: bool = True
    in_master: bool = True


V = {
    "shakti": Vendor("shakti", "Shakti Steel Traders", gstin("27AAKFS3317M1Z"), "Gala 14, Bhosari MIDC, Pune, Maharashtra 411026", "+91 20 2712 4410"),
    "bharat": Vendor("bharat", "Bharat Electricals & Controls", gstin("29AABCB5521K1Z"), "No. 45, 2nd Cross, Rajajinagar, Bengaluru, Karnataka 560010", "+91 80 2335 1180"),
    "orion": Vendor("orion", "Orion Packaging Solutions LLP", gstin("24AAGFO8812Q1Z"), "Survey 211, Changodar GIDC, Ahmedabad, Gujarat 382213", "+91 79 2696 0034"),
    "nilgiri": Vendor("nilgiri", "Nilgiri Lab & Safety Supplies", gstin("33AAHCN4471L1Z"), "18 Thiru Vi Ka Industrial Estate, Guindy, Chennai, Tamil Nadu 600032", "+91 44 2250 7791"),
    "ganga": Vendor("ganga", "Ganga Office Mart", gstin("29ABZPG7720D1Z"), "112, 4th Main, Yeshwanthpur, Bengaluru 560022", "98450 11223"),
    "kaveri": Vendor("kaveri", "Kaveri Tools & Hardware", gstin("29AAQPK3409H1Z"), "7/2 Old Madras Road, KR Puram, Bengaluru 560036", "99001 44556"),
    "apex": Vendor("apex", "Apex Industrial Traders", gstin("27AAMFA6630R1Z"), "Unit 9, Turbhe MIDC, Navi Mumbai, Maharashtra 400705", "+91 22 2768 3301", in_master=False),
}


@dataclass
class Item:
    code: str
    desc: str
    hsn: str
    unit: str
    rate: float
    gst: float = 18


@dataclass
class PO:
    no: str
    vendor: str
    date: str  # ISO
    lines: list  # (Item, qty)
    grn: str = ""
    grn_date: str = ""
    accepted: dict = field(default_factory=dict)  # line index -> accepted qty (default all)


CAT = {
    "ms_plate": Item("MSP-10", "MS Plate 10mm IS2062", "72085120", "Kg", 68.50),
    "ms_angle": Item("MSA-50", "MS Angle 50x50x6", "72162100", "Kg", 71.20),
    "gi_pipe": Item("GIP-25", "GI Pipe 25NB Medium", "73063090", "Mtr", 182.00),
    "flat_bar": Item("MSF-40", "MS Flat 40x6", "72155090", "Kg", 69.75),
    "mcb32": Item("MCB-32A", "MCB Double Pole 32A", "85362030", "Nos", 525.00),
    "cable4": Item("CBL-4SQ", "Copper Cable 4 sq mm (90m)", "85444999", "Roll", 6150.00),
    "contactor": Item("CTR-25", "Power Contactor 25A 3P", "85364900", "Nos", 1340.00),
    "led_panel": Item("LED-36W", "LED Panel Light 36W 2x2", "94054900", "Nos", 1120.00),
    "box_5ply": Item("CB-5P", "Corrugated Box 5 Ply 600x400x400", "48191010", "Nos", 48.60),
    "box_3ply": Item("CB-3P", "Corrugated Box 3 Ply 400x300x300", "48191010", "Nos", 22.40),
    "stretch": Item("SF-23", "Stretch Film 23 micron 500mm", "39201012", "Roll", 465.00),
    "tape": Item("BT-48", "BOPP Tape 48mm x 65m Brown", "39191000", "Nos", 38.00),
    "bubble": Item("BW-1M", "Bubble Wrap Roll 1m x 100m", "39211900", "Roll", 1180.00),
    "edge": Item("EP-50", "Paper Edge Protector 50x50x1000", "48239090", "Nos", 14.50),
    "strap": Item("PS-12", "PP Strapping Roll 12mm", "39269099", "Roll", 890.00),
    "pallet": Item("PL-WD", "Wooden Pallet 1200x1000 4-way", "44152000", "Nos", 1450.00, 12),
    "gloves": Item("NG-M", "Nitrile Gloves Medium (box of 100)", "40151900", "Box", 395.00, 12),
    "goggles": Item("SG-CL", "Safety Goggles Clear Anti-fog", "90049090", "Nos", 165.00),
    "helmet": Item("SH-YL", "Safety Helmet Yellow with Ratchet", "65061090", "Nos", 240.00),
    "beaker": Item("BK-500", "Borosilicate Beaker 500ml", "70179090", "Nos", 210.00, 12),
    "a4": Item("A4-75", "A4 Copier Paper 75 GSM (Ream)", "48025690", "Ream", 265.00, 12),
    "toner": Item("TN-88A", "Toner Cartridge 88A Compatible", "84439959", "Nos", 1450.00),
    "files": Item("BF-FS", "Box File Foolscap", "48201090", "Nos", 85.00),
    "drill": Item("DR-13", "Impact Drill 13mm 650W", "84672100", "Nos", 3250.00),
    "spanner": Item("SP-SET", "Combination Spanner Set 8-24mm", "82041120", "Set", 1180.00),
    "grinder": Item("AG-4", "Angle Grinder 4 inch 850W", "84672900", "Nos", 2890.00),
    "shoes": Item("SS-S3", "Safety Shoes S3 Steel Toe", "64019910", "Pair", 1180.00),
    "earplug": Item("EP-FM", "Foam Ear Plugs (pair)", "39269099", "Pair", 9.50),
    "dbox": Item("DB-8W", "Distribution Board 8 Way TPN", "85371000", "Nos", 4150.00),
}


def it(k, q):
    return (CAT[k], q)


POS = [
    PO("PO/26-27/0412", "shakti", "2026-10-06", [it("ms_plate", 1200), it("ms_angle", 800), it("gi_pipe", 150)], "GRN/26-27/1051", "2026-10-20"),
    PO("PO/26-27/0415", "shakti", "2026-10-09", [it("flat_bar", 600), it("ms_plate", 400)], "GRN/26-27/1058", "2026-10-24"),
    PO("PO/26-27/0418", "bharat", "2026-10-07", [it("mcb32", 40), it("cable4", 10), it("contactor", 12)], "GRN/26-27/1053", "2026-10-21"),
    PO("PO/26-27/0421", "bharat", "2026-10-12", [it("led_panel", 60), it("mcb32", 20)], "GRN/26-27/1062", "2026-10-27", accepted={0: 54}),
    PO("PO/26-27/0425", "orion", "2026-10-08", [it("box_5ply", 2000), it("box_3ply", 3000), it("stretch", 120), it("tape", 600), it("bubble", 25),
                                                  it("edge", 1500), it("strap", 40), it("pallet", 80)], "GRN/26-27/1055", "2026-10-22"),
    PO("PO/26-27/0428", "orion", "2026-10-14", [it("box_5ply", 1000), it("tape", 300)], "GRN/26-27/1064", "2026-10-28"),
    PO("PO/26-27/0430", "nilgiri", "2026-10-10", [it("gloves", 120), it("goggles", 200), it("helmet", 150), it("beaker", 60)], "GRN/26-27/1057", "2026-10-23"),
    PO("PO/26-27/0433", "nilgiri", "2026-10-26", [it("gloves", 80), it("helmet", 100)]),  # not yet received
    PO("PO/26-27/0436", "ganga", "2026-10-15", [it("a4", 100), it("toner", 6), it("files", 50)], "GRN/26-27/1060", "2026-10-25"),
    PO("PO/26-27/0439", "kaveri", "2026-10-16", [it("drill", 4), it("spanner", 6), it("grinder", 5)], "GRN/26-27/1061", "2026-10-26"),
    # edge cases: ordinary POs/GRNs, nothing special about them in the master data
    PO("PO/26-27/0442", "bharat", "2026-10-19", [it("cable4", 30), it("dbox", 10)], "GRN/26-27/1066", "2026-10-30"),
    PO("PO/26-27/0445", "nilgiri", "2026-10-20", [it("shoes", 50), it("earplug", 500)], "GRN/26-27/1067", "2026-10-31"),
    PO("PO/26-27/0447", "shakti", "2026-10-21", [it("gi_pipe", 100)], "GRN/26-27/1069", "2026-11-01"),
    PO("PO/26-27/0448", "shakti", "2026-10-22", [it("gi_pipe", 100)], "GRN/26-27/1070", "2026-11-01"),
]
# Extra POs/GRNs for the email demo: a small "new ERP export" in data/demo/email/, kept out of the main CSVs
EMAIL_POS = [
    PO("PO/26-27/0451", "orion", "2026-10-27", [it("box_3ply", 1500), it("stretch", 60), it("tape", 400)], "GRN/26-27/1074", "2026-11-06"),
    PO("PO/26-27/0453", "bharat", "2026-10-28", [it("led_panel", 30), it("contactor", 8)], "GRN/26-27/1075", "2026-11-07"),
]
POMAP = {p.no: p for p in POS + EMAIL_POS}


@dataclass
class Inv:
    file: str
    layout: str
    vendor: str
    no: str
    date: str  # ISO
    po: str
    lines: list  # (Item, qty, rate override or None, gst override or None)
    expect: str
    why: str


def L(k, q, rate=None, gst=None):
    return (CAT[k], q, rate, gst)


EMAIL_INVOICES = None  # filled below (needs Inv)


INVOICES = [
    Inv("01_shakti_clean.pdf", "classic", "shakti", "SST/26-27/0871", "2026-10-28", "PO/26-27/0412",
        [L("ms_plate", 1200), L("ms_angle", 800), L("gi_pipe", 150)], "APPROVE", "Everything matches PO and GRN (inter-state, IGST)."),
    Inv("02_shakti_price_above_po.pdf", "classic", "shakti", "SST/26-27/0884", "2026-11-02", "PO/26-27/0415",
        [L("flat_bar", 600, rate=74.25), L("ms_plate", 400)], "HOLD", "MS Flat billed at 74.25/kg against PO price 69.75 (PRICE_MISMATCH)."),
    Inv("03_bharat_clean.pdf", "tally", "bharat", "BEC/26-27/778", "2026-10-29", "PO/26-27/0418",
        [L("mcb32", 40), L("cable4", 10), L("contactor", 12)], "APPROVE", "Tally-style bill, intra-state CGST + SGST; matches PO and GRN."),
    Inv("04_bharat_short_receipt.pdf", "tally", "bharat", "BEC/26-27/791", "2026-11-03", "PO/26-27/0421",
        [L("led_panel", 60), L("mcb32", 20)], "HOLD", "Bills 60 LED panels but the GRN accepted only 54 (6 rejected): QUANTITY_EXCEEDS_GRN."),
    Inv("05_orion_eight_lines.pdf", "modern", "orion", "OPS-2026-1187", "2026-10-30", "PO/26-27/0425",
        [L("box_5ply", 2000), L("box_3ply", 3000), L("stretch", 120), L("tape", 600), L("bubble", 25), L("edge", 1500), L("strap", 40), L("pallet", 80)],
        "APPROVE", "Modern layout, 8 lines with mixed 18% and 12% GST; matches PO and GRN."),
    Inv("06_orion_wrong_tax_rate.pdf", "modern", "orion", "OPS-2026-1203", "2026-11-04", "PO/26-27/0428",
        [L("box_5ply", 1000, gst=12), L("tape", 300)], "HOLD", "Boxes charged at 12% GST where the PO says 18% (TAX_RATE_MISMATCH)."),
    Inv("07_nilgiri_hsn_summary.pdf", "grid", "nilgiri", "NLS/1456/26-27", "2026-10-31", "PO/26-27/0430",
        [L("gloves", 120), L("goggles", 200), L("helmet", 150), L("beaker", 60)], "APPROVE", "Boxed layout with an HSN-wise tax summary under the items; matches."),
    Inv("08_nilgiri_goods_not_received.pdf", "grid", "nilgiri", "NLS/1471/26-27", "2026-11-05", "PO/26-27/0433",
        [L("gloves", 80), L("helmet", 100)], "PENDING_DOCUMENTS", "PO exists but nothing has been received yet (GRN_NOT_RECEIVED); clears when a GRN arrives."),
    Inv("09_ganga_handwritten.pdf", "handwritten", "ganga", "GOM-0342", "2026-10-30", "PO/26-27/0436",
        [L("a4", 100), L("toner", 6), L("files", 50)], "APPROVE or REVIEW", "Handwritten bill-book scan, read by the vision model. Approves if every field is read and reconciles; otherwise REVIEW for a clerk to confirm."),
    Inv("10_kaveri_handwritten.pdf", "handwritten2", "kaveri", "KTH/219", "2026-11-01", "PO/26-27/0439",
        [L("drill", 4), L("spanner", 6), L("grinder", 5)], "APPROVE or REVIEW", "Second handwriting style, slightly skewed scan; vision model."),
    Inv("11_apex_unknown_vendor.pdf", "classic", "apex", "AIT/26-27/0055", "2026-11-05", "",
        [L("ms_plate", 500), L("gi_pipe", 40)], "PENDING_DOCUMENTS",
        "Supplier is not in the vendor master (VENDOR_NOT_FOUND) and no PO exists (PO_NOT_FOUND); waits for procurement, never auto-approved."),
    # --- edge case 1: split invoices against one PO (upload in order)
    Inv("12_bharat_split_part1.pdf", "tally", "bharat", "BEC/26-27/805", "2026-11-04", "PO/26-27/0442",
        [L("cable4", 18), L("dbox", 6)], "APPROVE", "Edge 1: first partial invoice on PO/26-27/0442 (18 of 30 cable rolls, 6 of 10 boards)."),
    Inv("13_bharat_split_part2.pdf", "tally", "bharat", "BEC/26-27/812", "2026-11-06", "PO/26-27/0442",
        [L("cable4", 12), L("dbox", 4)], "APPROVE", "Edge 1: second partial invoice closes the PO exactly (cumulative 30 and 10)."),
    Inv("14_bharat_split_overbill.pdf", "tally", "bharat", "BEC/26-27/819", "2026-11-08", "PO/26-27/0442",
        [L("cable4", 5)], "HOLD", "Edge 1: looks fine alone, but the PO is already fully invoiced (QUANTITY_EXCEEDS_PO / GRN)."),
    # --- edge case 2: no PO number printed on the invoice
    Inv("15_nilgiri_no_po_number.pdf", "grid", "nilgiri", "NLS/1488/26-27", "2026-11-05", "",
        [L("shoes", 50), L("earplug", 500)], "APPROVE", "Edge 2: no PO reference; PO/26-27/0445 is the only open PO of this supplier matching every line, so it is used and the reasoning shown."),
    Inv("16_shakti_no_po_ambiguous.pdf", "classic", "shakti", "SST/26-27/0902", "2026-11-06", "",
        [L("gi_pipe", 100)], "REVIEW", "Edge 2: no PO reference and two identical open POs (0447, 0448): the system will not guess; the clerk picks one and it re-checks to APPROVE."),
    # --- edge case 3: duplicate resubmission under a new number (exact re-upload of any file is the other variant)
    Inv("17_bharat_renumbered_duplicate.pdf", "tally", "bharat", "BEC/26-27/778A", "2026-10-29", "PO/26-27/0418",
        [L("mcb32", 40), L("cable4", 10), L("contactor", 12)], "HOLD", "Edge 3: same bill as 03 re-sent as 778A (same supplier, date, amount): POSSIBLE_DUPLICATE, and the PO is already fully invoiced."),
]

# Clean invoices to send by email during the demo (import email/new_purchase_orders.csv + new_goods_receipt_notes.csv first)
EMAIL_INVOICES = [
    Inv("18_shakti_gi_pipe.pdf", "classic", "shakti", "SST/26-27/0915", "2026-11-08", "PO/26-27/0448",
        [L("gi_pipe", 100)], "APPROVE", "Second GI-pipe PO (0448), still unbilled once 16 is matched to 0447. No import needed."),
    Inv("19_orion_packaging.pdf", "modern", "orion", "OPS-2026-1229", "2026-11-09", "PO/26-27/0451",
        [L("box_3ply", 1500), L("stretch", 60), L("tape", 400)], "APPROVE", "Matches PO/26-27/0451 and GRN/26-27/1074 (in the email import files)."),
    Inv("20_bharat_lighting.pdf", "tally", "bharat", "BEC/26-27/833", "2026-11-10", "PO/26-27/0453",
        [L("led_panel", 30), L("contactor", 8)], "APPROVE", "Matches PO/26-27/0453 and GRN/26-27/1075 (in the email import files)."),
]


# ---------- helpers ----------
def money(x):
    neg = x < 0
    s = f"{abs(x):.2f}"
    i, d = s[:-3], s[-3:]
    if len(i) > 3:
        head, tail = i[:-3], i[-3:]
        parts = []
        while len(head) > 2:
            parts.insert(0, head[-2:])
            head = head[:-2]
        if head:
            parts.insert(0, head)
        i = ",".join(parts + [tail])
    return ("-" if neg else "") + i + d


def r2(x):
    return math.floor(x * 100 + 0.5) / 100


def dmy(iso, sep="/"):
    y, m, d = iso.split("-")
    return f"{d}{sep}{m}{sep}{y}"


def dmon(iso):
    y, m, d = iso.split("-")
    return f"{int(d)}-{['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'][int(m)-1]}-{y}"


ONES = "Zero One Two Three Four Five Six Seven Eight Nine Ten Eleven Twelve Thirteen Fourteen Fifteen Sixteen Seventeen Eighteen Nineteen".split()
TENS = "_ _ Twenty Thirty Forty Fifty Sixty Seventy Eighty Ninety".split()


def words(n):
    n = int(n)

    def two(x):
        return ONES[x] if x < 20 else TENS[x // 10] + ("" if x % 10 == 0 else " " + ONES[x % 10])

    def three(x):
        h, r = divmod(x, 100)
        return ((ONES[h] + " Hundred" + (" " if r else "")) if h else "") + (two(r) if r else "")
    out = []
    for div, name in ((10**7, "Crore"), (10**5, "Lakh"), (1000, "Thousand")):
        q, n = divmod(n, div)
        if q:
            out.append(three(q) + " " + name)
    if n:
        out.append(three(n))
    return " ".join(out) or "Zero"


def compute(inv):
    v = V[inv.vendor]
    intra = v.gstin[:2] == BUYER["gstin"][:2]
    rows, taxable, tax = [], 0.0, 0.0
    by_rate, by_hsn = {}, {}
    for n, (item, q, rate, gst) in enumerate(inv.lines, 1):
        rate = item.rate if rate is None else rate
        gst = item.gst if gst is None else gst
        tv = r2(q * rate)
        ta = r2(tv * gst / 100)
        taxable += tv
        tax += ta
        by_rate[gst] = by_rate.get(gst, 0) + ta
        h = by_hsn.setdefault((item.hsn, gst), [0.0, 0.0])
        h[0] += tv
        h[1] += ta
        rows.append(dict(n=n, code=item.code, desc=item.desc, hsn=item.hsn, unit=item.unit, qty=q, rate=rate, gst=gst, taxable=tv, tax=ta, total=r2(tv + ta)))
    taxable, tax = r2(taxable), r2(tax)
    cgst = sgst = igst = 0.0
    if intra:
        cgst = r2(tax / 2)
        sgst = r2(tax - cgst)
    else:
        igst = tax
    gt = r2(taxable + tax)
    ro = r2(round(gt) - gt)
    return dict(v=v, intra=intra, rows=rows, taxable=taxable, tax=tax, cgst=cgst, sgst=sgst, igst=igst, ro=ro, total=r2(gt + ro),
                by_rate=by_rate, by_hsn=by_hsn)


E = html.escape
BASE_CSS = """
@page { size: A4; margin: 14mm 12mm; }
* { box-sizing: border-box; }
body { font-family: 'Helvetica Neue', Arial, sans-serif; font-size: 10.5px; color: #111; margin: 0; }
table { border-collapse: collapse; width: 100%; }
.r { text-align: right; } .c { text-align: center; } .b { font-weight: 700; }
"""


def qty(q):
    return f"{q:g}"


# ---------- layout 1: classic bordered ----------
def classic(inv, c):
    v = c["v"]
    rows = "".join(f"<tr><td class=c>{r['n']}</td><td>{E(r['desc'])}<br><small>{r['code']}</small></td><td class=c>{r['hsn']}</td><td class=r>{qty(r['qty'])}</td>"
                   f"<td class=c>{r['unit']}</td><td class=r>{money(r['rate'])}</td><td class=r>{money(r['taxable'])}</td><td class=c>{r['gst']:g}%</td>"
                   f"<td class=r>{money(r['tax'])}</td><td class=r>{money(r['total'])}</td></tr>" for r in c["rows"])
    po = f"<tr><td>PO No.</td><td class=b>{inv.po}</td></tr>" if inv.po else ""
    taxrows = (f"<tr><td>CGST</td><td class=r>{money(c['cgst'])}</td></tr><tr><td>SGST</td><td class=r>{money(c['sgst'])}</td></tr>" if c["intra"]
               else f"<tr><td>IGST</td><td class=r>{money(c['igst'])}</td></tr>")
    return f"""<style>{BASE_CSS}
.box {{ border: 1px solid #333; }} .hd td {{ vertical-align: top; padding: 6px 8px; }}
.items th, .items td {{ border: 1px solid #555; padding: 4px 5px; }} .items th {{ background: #e9e9e9; font-size: 9.5px; }}
.meta td {{ padding: 2px 4px; }} .tot td {{ padding: 3px 8px; }}
</style>
<div class=c style="font-size:17px;font-weight:700;letter-spacing:1px;margin-bottom:6px">TAX INVOICE</div>
<table class="box hd"><tr>
<td style="width:55%;border-right:1px solid #333"><div style="font-size:15px" class=b>{E(v.name)}</div>{E(v.addr)}<br>Phone: {v.phone}<br>GSTIN: <b>{v.gstin}</b></td>
<td><table class=meta><tr><td>Invoice No.</td><td class=b>{inv.no}</td></tr><tr><td>Invoice Date</td><td class=b>{dmy(inv.date)}</td></tr>{po}
<tr><td>Place of Supply</td><td>Karnataka (29)</td></tr><tr><td>Payment Terms</td><td>Net 30 days</td></tr></table></td></tr>
<tr><td colspan=2 style="border-top:1px solid #333"><b>Bill To:</b> {E(BUYER['name'])}, {E(BUYER['addr'])}<br>GSTIN: <b>{BUYER['gstin']}</b></td></tr></table>
<table class=items style="margin-top:8px"><thead><tr><th>S.No</th><th>Description</th><th>HSN</th><th>Qty</th><th>Unit</th><th>Rate</th><th>Taxable Value</th><th>GST %</th><th>Tax Amount</th><th>Total</th></tr></thead>
<tbody>{rows}</tbody></table>
<table style="margin-top:8px"><tr><td style="width:55%;vertical-align:top">Amount in words:<br><b>Indian Rupees {words(c['total'])} Only</b>
<p style="margin-top:14px">Bank: State Bank of India, A/c 3914 2277 5501, IFSC SBIN0001234</p></td>
<td><table class="box tot"><tr><td>Taxable Value</td><td class=r>{money(c['taxable'])}</td></tr>{taxrows}
<tr><td>Round Off</td><td class=r>{money(c['ro'])}</td></tr><tr style="border-top:1px solid #333"><td class=b>Grand Total</td><td class="r b">{money(c['total'])}</td></tr></table></td></tr></table>
<div class=r style="margin-top:40px">For {E(v.name)}<br><br><br>Authorised Signatory</div>"""


# ---------- layout 2: Tally-style ----------
def tally(inv, c):
    v = c["v"]
    rows = "".join(f"<tr><td class=c>{r['n']}</td><td><b>{E(r['desc'])}</b></td><td class=c>{r['hsn']}</td><td class=r>{qty(r['qty'])} {r['unit']}</td>"
                   f"<td class=r>{money(r['rate'])}</td><td class=c>{r['unit']}</td><td class=r>{money(r['taxable'])}</td></tr>" for r in c["rows"])
    return f"""<style>{BASE_CSS}
body {{ font-family: Arial, sans-serif; }} .g td {{ border: 1px solid #000; padding: 3px 5px; vertical-align: top; }}
.it th {{ border: 1px solid #000; padding: 3px; font-weight: 700; }} .it td {{ border-left: 1px solid #000; border-right: 1px solid #000; padding: 3px 5px; }}
.it tr:last-child td {{ border-bottom: 1px solid #000; }} small {{ color: #333; }}
</style>
<div class=c style="font-size:14px;font-weight:700">Tax Invoice</div>
<table class=g><tr><td rowspan=3 style="width:50%"><b style="font-size:13px">{E(v.name)}</b><br>{E(v.addr)}<br>GSTIN/UIN: {v.gstin}<br>State Name: Karnataka, Code: 29</td>
<td>Invoice No.<br><b>{inv.no}</b></td><td>Dated<br><b>{dmon(inv.date)}</b></td></tr>
<tr><td>Buyer's Order No.<br><b>{inv.po}</b></td><td>Dated<br><b>{dmon(POMAP[inv.po].date)}</b></td></tr>
<tr><td>Mode/Terms of Payment<br>30 Days</td><td>Dispatched through<br>Own vehicle</td></tr>
<tr><td colspan=3>Buyer (Bill to)<br><b>{E(BUYER['name'])}</b><br>{E(BUYER['addr'])}<br>GSTIN/UIN: {BUYER['gstin']}</td></tr></table>
<table class=it><thead><tr><th>Sl No.</th><th>Description of Goods</th><th>HSN/SAC</th><th>Quantity</th><th>Rate</th><th>per</th><th>Amount</th></tr></thead>
<tbody>{rows}<tr><td></td><td></td><td></td><td></td><td></td><td></td><td style="height:40px"></td></tr></tbody></table>
<table class=g style="margin-top:6px;width:55%;margin-left:45%">
<tr><td>Taxable Value</td><td class=r>{money(c['taxable'])}</td></tr>
<tr><td>CGST{half_rate(c)}</td><td class=r>{money(c['cgst'])}</td></tr>
<tr><td>SGST{half_rate(c)}</td><td class=r>{money(c['sgst'])}</td></tr>
<tr><td>Round Off</td><td class=r>{money(c['ro'])}</td></tr>
<tr><td class=b>Total</td><td class="r b">₹ {money(c['total'])}</td></tr></table>
<p>Amount Chargeable (in words)<br><b>INR {words(c['total'])} Only</b></p>
<p style="margin-top:10px">Company's PAN: {v.gstin[2:12]}<br>Declaration: We declare that this invoice shows the actual price of the goods described and that all particulars are true and correct.</p>
<div class=r style="margin-top:30px">for {E(v.name)}<br><br><br>Authorised Signatory</div>
<div class=c style="margin-top:10px"><small>This is a Computer Generated Invoice</small></div>"""


# ---------- layout 3: modern, colour banner, totals card ----------
def modern(inv, c):
    v = c["v"]
    rows = "".join(f"<tr><td>{r['n']:02d}</td><td><div class=b>{E(r['desc'])}</div><div class=mut>SKU {r['code']}</div></td><td>{r['hsn']}</td>"
                   f"<td class=r>{qty(r['qty'])}</td><td>{r['unit']}</td><td class=r>{money(r['rate'])}</td><td class=r>{r['gst']:g}%</td><td class=r>{money(r['taxable'])}</td></tr>"
                   for r in c["rows"])
    rates = "".join(f"<tr><td>IGST @ {k:g}%</td><td class=r>{money(r2(t))}</td></tr>" for k, t in sorted(c["by_rate"].items()))
    return f"""<style>{BASE_CSS}
body {{ font-family: 'DejaVu Sans', Verdana, sans-serif; font-size: 10px; color: #1d2433; }}
.band {{ background: #0f4c81; color: #fff; padding: 14px 16px; display: flex; justify-content: space-between; align-items: flex-end; }}
.band h1 {{ margin: 0; font-size: 22px; letter-spacing: 3px; }} .mut {{ color: #6b7280; font-size: 9px; }}
.two {{ display: flex; gap: 24px; margin: 14px 0; }} .two > div {{ flex: 1; }} .lab {{ text-transform: uppercase; font-size: 8.5px; color: #0f4c81; font-weight: 700; letter-spacing: .6px; }}
.items th {{ text-align: left; background: #eef3f8; color: #0f4c81; padding: 6px 5px; font-size: 9px; }} .items td {{ padding: 6px 5px; border-bottom: 1px solid #e5e7eb; }}
.items th.r {{ text-align: right; }} .card {{ width: 44%; margin-left: 56%; margin-top: 10px; background: #f6f8fb; padding: 8px 10px; }}
.card td {{ padding: 3px 0; }} .due td {{ font-size: 13px; font-weight: 700; color: #0f4c81; border-top: 2px solid #0f4c81; padding-top: 6px; }}
</style>
<div class=band><div><h1>INVOICE</h1><div>{E(v.name)}</div></div><div class=r>Invoice # <b>{inv.no}</b><br>Issue date: {dmy(inv.date, '.')}{"<br>Purchase order: " + inv.po if inv.po else ""}</div></div>
<div class=two><div><div class=lab>From</div><b>{E(v.name)}</b><br>{E(v.addr)}<br>GSTIN {v.gstin}<br>{v.phone}</div>
<div><div class=lab>Billed to</div><b>{E(BUYER['name'])}</b><br>{E(BUYER['addr'])}<br>GSTIN {BUYER['gstin']}</div>
<div><div class=lab>Payment</div>Due in 45 days<br>HDFC Bank 50200011223344<br>IFSC HDFC0000240</div></div>
<table class=items><thead><tr><th>#</th><th>Item</th><th>HSN</th><th class=r>Qty</th><th>Unit</th><th class=r>Unit price</th><th class=r>GST</th><th class=r>Amount</th></tr></thead><tbody>{rows}</tbody></table>
<div class=card><table><tr><td>Subtotal</td><td class=r>{money(c['taxable'])}</td></tr>{rates}
<tr><td>Total IGST</td><td class=r>{money(c['igst'])}</td></tr><tr><td>Rounding</td><td class=r>{money(c['ro'])}</td></tr>
<tr class=due><td>Total due (INR)</td><td class=r>{money(c['total'])}</td></tr></table></div>
<p class=mut style="margin-top:24px">Goods once sold will be taken back only against a debit note within 7 days. Subject to Ahmedabad jurisdiction.</p>"""


# ---------- layout 4: boxed grid with HSN summary ----------
def grid(inv, c):
    v = c["v"]
    rows = "".join(f"<tr><td class=c>{r['n']}</td><td>{r['code']}</td><td>{E(r['desc'])}</td><td class=c>{r['hsn']}</td><td class=r>{qty(r['qty'])}</td>"
                   f"<td class=c>{r['unit']}</td><td class=r>{money(r['rate'])}</td><td class=r>{money(r['taxable'])}</td></tr>" for r in c["rows"])
    hsn = "".join(f"<tr><td>{h}</td><td class=r>{money(r2(a[0]))}</td><td class=c>{g:g}%</td><td class=r>{money(r2(a[1]))}</td></tr>" for (h, g), a in sorted(c["by_hsn"].items()))
    return f"""<style>{BASE_CSS}
body {{ font-family: 'Liberation Serif', 'Times New Roman', serif; font-size: 11px; }}
td, th {{ border: 1px solid #222; padding: 4px 5px; }} th {{ background: #f1efe6; }} .nob td {{ border: none; padding: 1px 0; }}
h2 {{ margin: 0; font-size: 18px; }}
</style>
<table><tr><td style="width:62%;border-right:none"><h2>{E(v.name)}</h2>{E(v.addr)}<br>Ph: {v.phone} &nbsp; GSTIN: {v.gstin}</td>
<td class=c style="border-left:none"><div style="font-size:16px;font-weight:700">GST INVOICE</div>Original for Recipient</td></tr></table>
<table style="margin-top:-1px"><tr><td style="width:50%">Consignee / Buyer:<br><b>{E(BUYER['name'])}</b><br>{E(BUYER['addr'])}<br>GSTIN: {BUYER['gstin']}</td>
<td><table class=nob><tr><td>Bill No</td><td>: <b>{inv.no}</b></td></tr><tr><td>Bill Date</td><td>: {dmy(inv.date, '-')}</td></tr>
{f"<tr><td>Order Ref</td><td>: {inv.po}</td></tr><tr><td>Order Date</td><td>: {dmy(POMAP[inv.po].date, '-')}</td></tr>" if inv.po else "<tr><td>Payment</td><td>: 30 days</td></tr>"}</table></td></tr></table>
<table style="margin-top:8px"><thead><tr><th>Sr</th><th>Item Code</th><th>Particulars</th><th>HSN</th><th>Qty</th><th>UOM</th><th>Rate</th><th>Value</th></tr></thead><tbody>{rows}</tbody></table>
<table style="margin-top:8px"><tr><td style="width:58%;vertical-align:top;padding:0;border:none">
<table><thead><tr><th>HSN</th><th>Taxable</th><th>IGST Rate</th><th>IGST Amt</th></tr></thead><tbody>{hsn}</tbody></table>
<p>Rupees {words(c['total'])} Only</p></td><td style="vertical-align:top;padding:0;border:none">
<table><tr><td>Total Taxable</td><td class=r>{money(c['taxable'])}</td></tr><tr><td>IGST</td><td class=r>{money(c['igst'])}</td></tr>
<tr><td>Round Off</td><td class=r>{money(c['ro'])}</td></tr><tr><td class=b>Net Amount</td><td class="r b">{money(c['total'])}</td></tr></table></td></tr></table>
<p style="margin-top:30px" class=r>For {E(v.name)}<br><br><br>Proprietor / Authorised Signatory</p>"""


# ---------- layouts 5/6: handwritten bill book ----------
def half_rate(c):
    rates = {r["gst"] for r in c["rows"]}
    return f" @ {rates.pop() / 2:g}%" if len(rates) == 1 else ""


def handwritten(inv, c, font, ink, paper):
    v = c["v"]
    rnd = random.Random(inv.no)
    j = lambda: f"transform: rotate({rnd.uniform(-1.4, 1.4):.2f}deg) translateY({rnd.uniform(-1.5, 1.5):.1f}px); display:inline-block"
    rows = "".join(f"<tr><td class=c>{r['n']}</td><td><span style='{j()}'>{E(r['desc'])}</span></td><td class=c><span style='{j()}'>{r['hsn']}</span></td>"
                   f"<td class=c><span style='{j()}'>{qty(r['qty'])} {r['unit']}</span></td><td class=r><span style='{j()}'>{money(r['rate'])}</span></td>"
                   f"<td class=r><span style='{j()}'>{money(r['taxable'])}</span></td></tr>" for r in c["rows"])
    blank = "<tr><td>&nbsp;</td><td></td><td></td><td></td><td></td><td></td></tr>" * 4
    return f"""<link href="https://fonts.googleapis.com/css2?family={font.replace(' ', '+')}&display=block" rel="stylesheet">
<style>{BASE_CSS}
body {{ background: {paper}; font-size: 11px; font-family: Georgia, serif; color: #2b2b2b; padding: 6px; }}
.hw {{ font-family: '{font}', cursive; color: {ink}; font-size: 17px; }}
.pre {{ border: 2px solid #8a1c1c; padding: 8px; }} .pre td, .pre th {{ border: 1px solid #8a1c1c; padding: 3px 6px; }}
.red {{ color: #8a1c1c; }} th {{ color: #8a1c1c; font-weight: 700; font-size: 11px; }}
.items td {{ height: 30px; }}
</style>
<div class=pre>
<div class="c red" style="font-size:20px;font-weight:700;letter-spacing:1px">{E(v.name.upper())}</div>
<div class="c red">{E(v.addr)} &nbsp;|&nbsp; Ph: {v.phone}</div>
<div class="c red">GSTIN: {v.gstin}</div>
<div class="c red" style="font-weight:700;margin:4px 0;font-size:13px">TAX INVOICE / CASH-CREDIT BILL</div>
<table><tr><td class=red style="width:50%">Bill No. <span class=hw style="{j()}">{inv.no}</span></td><td class="red r">Date <span class=hw style="{j()}">{dmy(inv.date)}</span></td></tr>
<tr><td class=red colspan=2>M/s. <span class=hw style="{j()}">{E(BUYER['name'])}, Peenya, Bengaluru</span></td></tr>
<tr><td class=red>Party GSTIN <span class=hw style="{j()}">{BUYER['gstin']}</span></td><td class="red r">P.O. No. <span class=hw style="{j()}">{inv.po}</span></td></tr></table>
<table class=items style="margin-top:6px"><thead><tr><th style="width:6%">Sl.</th><th>Particulars</th><th>HSN</th><th>Qty</th><th>Rate</th><th>Amount</th></tr></thead>
<tbody class=hw>{rows}{blank}</tbody></table>
<table style="margin-top:6px"><tr><td class=red style="width:55%" rowspan=5>Rupees <span class=hw style="{j()}">{words(c['total'])} only</span>
<br><br><br><span class=red>Customer's signature</span></td>
<td class=red>Total</td><td class="r hw">{money(c['taxable'])}</td></tr>
<tr><td class=red>CGST{half_rate(c)}</td><td class="r hw">{money(c['cgst'])}</td></tr>
<tr><td class=red>SGST{half_rate(c)}</td><td class="r hw">{money(c['sgst'])}</td></tr>
<tr><td class=red>Round Off</td><td class="r hw">{money(c['ro'])}</td></tr>
<tr><td class=red><b>G. Total</b></td><td class="r hw" style="font-size:20px">{money(c['total'])}</td></tr></table>
<div class="r red" style="margin-top:8px">For {E(v.name)}<br><span class=hw style="font-size:24px;{j()}">{v.name.split()[0]}</span></div>
</div>"""


LAYOUTS = {
    "classic": classic, "tally": tally, "modern": modern, "grid": grid,
    "handwritten": lambda i, c: handwritten(i, c, "Kalam", "#1a2a8c", "#fffdf4"),
    "handwritten2": lambda i, c: handwritten(i, c, "Shadows Into Light", "#202020", "#f8f6ee"),
}


def scanify(pdf, out, seed):
    """Rasterise to a scanned-looking, image-only PDF (no text layer)."""
    rnd = random.Random(seed)
    with tempfile.TemporaryDirectory() as td:
        subprocess.run(["pdftoppm", "-r", "150", "-png", pdf, f"{td}/p"], check=True)
        pages = sorted(f for f in os.listdir(td) if f.endswith(".png"))
        outs = []
        for k, p in enumerate(pages):
            o = f"{td}/s{k}.jpg"
            subprocess.run(["magick", f"{td}/{p}", "-background", "#f4f1e6", "-rotate", f"{rnd.uniform(-1.6, 1.6):.2f}",
                            "-attenuate", "0.35", "+noise", "Gaussian", "-blur", "0x0.5", "-modulate", "100,85",
                            "-quality", "72", o], check=True)
            outs.append(o)
        subprocess.run(["img2pdf", "--pagesize", "A4", "-o", out, *outs], check=True)


def write_csvs():
    write_po_grn_csvs(POS, f"{OUT}/purchase_orders.csv", f"{OUT}/goods_receipt_notes.csv")
    os.makedirs(f"{OUT}/email", exist_ok=True)
    write_po_grn_csvs(EMAIL_POS, f"{OUT}/email/new_purchase_orders.csv", f"{OUT}/email/new_goods_receipt_notes.csv")
    with open(f"{OUT}/vendors.csv", "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["legal_name", "gstin", "address", "is_approved", "vendor_status"])
        for v in V.values():
            if v.in_master:
                w.writerow([v.name, v.gstin, v.addr, "yes" if v.approved else "no", "ACTIVE"])


def write_po_grn_csvs(pos, po_path, grn_path):
    with open(po_path, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["po_number", "vendor_gstin", "po_date", "buyer_entity", "currency", "po_status", "line_number", "item_code", "description",
                    "hsn_sac", "quantity_ordered", "unit_of_measure", "unit_price", "tax_rate"])
        for p in pos:
            for n, (item, q) in enumerate(p.lines, 1):
                w.writerow([p.no, V[p.vendor].gstin, p.date, BUYER["name"], "INR", "APPROVED", n, item.code, item.desc, item.hsn, f"{q:g}", item.unit,
                            f"{item.rate:.2f}", f"{item.gst:g}"])
    with open(grn_path, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["grn_number", "po_number", "vendor_gstin", "receipt_date", "line_number", "item_code", "description",
                    "quantity_received", "quantity_accepted", "quantity_rejected", "unit_of_measure"])
        for p in pos:
            if not p.grn:
                continue
            for n, (item, q) in enumerate(p.lines, 1):
                acc = p.accepted.get(n - 1, q)
                w.writerow([p.grn, p.no, V[p.vendor].gstin, p.grn_date, n, item.code, item.desc, f"{q:g}", f"{acc:g}", f"{q - acc:g}", item.unit])


# Goods that arrive later in the demo: invoice 08 waits for this GRN. Not in goods_receipt_notes.csv on purpose;
# the manager imports the one-row sheet or uploads the PDF during the demo, and 08 re-checks to APPROVE.
LATER_GRN = dict(no="GRN/26-27/1072", po="PO/26-27/0433", date="2026-11-10", received_by="R. Kumar (Stores)")


def later_grn_rows():
    p = POMAP[LATER_GRN["po"]]
    return [(n, item, q) for n, (item, q) in enumerate(p.lines, 1)]


def grn_html():
    g, p = LATER_GRN, POMAP[LATER_GRN["po"]]
    v = V[p.vendor]
    rows = "".join(f"<tr><td class=c>{n}</td><td>{it.code}</td><td>{E(it.desc)}</td><td class=r>{qty(q)}</td><td class=r>{qty(q)}</td>"
                   f"<td class=r>{qty(q)}</td><td class=r>0</td><td class=c>{it.unit}</td></tr>" for n, it, q in later_grn_rows())
    tot = sum(q for _, _, q in later_grn_rows())
    return f"""<style>{BASE_CSS}
td, th {{ border: 1px solid #333; padding: 5px 6px; }} th {{ background: #ececec; }} .nob td {{ border: none; padding: 2px 4px; }}
</style>
<div class=c style="font-size:12px">{E(BUYER['name'])}, {E(BUYER['addr'])} | GSTIN {BUYER['gstin']}</div>
<div class=c style="font-size:17px;font-weight:700;margin:8px 0">GOODS RECEIPT NOTE</div>
<table class=nob><tr><td>GRN No: <b>{g['no']}</b></td><td class=r>GRN Date: <b>{dmy(g['date'])}</b></td></tr>
<tr><td>PO No: <b>{g['po']}</b></td><td class=r>PO Date: {dmy(p.date)}</td></tr>
<tr><td>Supplier: <b>{E(v.name)}</b></td><td class=r>Supplier GSTIN: {v.gstin}</td></tr>
<tr><td>Supplier Invoice / DC: NLS/1471/26-27</td><td class=r>Vehicle: TN 09 BQ 4471</td></tr></table>
<table style="margin-top:10px"><thead><tr><th>Sl No</th><th>Item Code</th><th>Description</th><th>Ordered Qty</th><th>Received Qty</th><th>Accepted Qty</th><th>Rejected Qty</th><th>UOM</th></tr></thead>
<tbody>{rows}<tr><td></td><td></td><td class=b>Total</td><td class=r>{qty(tot)}</td><td class=r>{qty(tot)}</td><td class=r>{qty(tot)}</td><td class=r>0</td><td></td></tr></tbody></table>
<p style="margin-top:14px">Remarks: Received in good condition. Quality check passed.</p>
<table class=nob style="margin-top:40px"><tr><td>Received by: {g['received_by']}</td><td class=r>Checked by: Quality Inspector</td></tr></table>"""


def write_later_grn(page):
    d = f"{OUT}/later"
    os.makedirs(d, exist_ok=True)
    g, p = LATER_GRN, POMAP[LATER_GRN["po"]]
    with open(f"{d}/grn_for_invoice_08.csv", "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["grn_number", "po_number", "vendor_gstin", "receipt_date", "line_number", "item_code", "description",
                    "quantity_received", "quantity_accepted", "quantity_rejected", "unit_of_measure"])
        for n, it, q in later_grn_rows():
            w.writerow([g["no"], g["po"], V[p.vendor].gstin, g["date"], n, it.code, it.desc, f"{q:g}", f"{q:g}", "0", it.unit])
    page.set_content("<!doctype html><html><head><meta charset=utf-8></head><body>" + grn_html() + "</body></html>", wait_until="networkidle")
    page.pdf(path=f"{d}/grn_for_invoice_08.pdf", format="A4", print_background=True)
    print("later/grn_for_invoice_08.csv + .pdf (GRN for PO/26-27/0433)")


def main():
    from playwright.sync_api import sync_playwright
    os.makedirs(f"{OUT}/invoices", exist_ok=True)
    write_csvs()
    with sync_playwright() as pw, tempfile.TemporaryDirectory() as td:
        browser = pw.chromium.launch()
        page = browser.new_page()
        for inv in INVOICES + EMAIL_INVOICES:
            c = compute(inv)
            page.set_content("<!doctype html><html><head><meta charset=utf-8></head><body>" + LAYOUTS[inv.layout](inv, c) + "</body></html>",
                             wait_until="networkidle")
            page.evaluate("document.fonts.ready")
            target = f"{OUT}/{'email' if inv in EMAIL_INVOICES else 'invoices'}/{inv.file}"
            if inv.layout.startswith("handwritten"):
                tmp = f"{td}/{inv.file}"
                page.pdf(path=tmp, format="A4", print_background=True)
                scanify(tmp, target, inv.no)
            else:
                page.pdf(path=target, format="A4", print_background=True)
            print(f"{inv.file:42s} {inv.layout:12s} total {money(c['total']):>14s}  expect {inv.expect}")
        write_later_grn(page)
        browser.close()
    with open(f"{OUT}/expected.csv", "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["file", "layout", "vendor", "invoice_number", "po_number", "grand_total", "expected_decision", "why"])
        for inv in INVOICES:
            c = compute(inv)
            w.writerow([inv.file, inv.layout, V[inv.vendor].name, inv.no, inv.po, f"{c['total']:.2f}", inv.expect, inv.why])
    print("written to", OUT)


if __name__ == "__main__":
    main()
