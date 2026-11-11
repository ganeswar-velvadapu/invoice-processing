"use client";

import { useState } from "react";
import { api, fileObjectURL } from "@/lib/api";
import { Button, Card, ErrorBox, Field, inputCls, PageHeader } from "@/components/ui";

const columns: Record<string, string> = {
  vendors: "legal_name*, gstin*, address, is_approved, vendor_status",
  purchase_orders: "po_number*, vendor_gstin*, po_date, buyer_entity, currency, po_status, line_number, item_code, description, hsn_sac, quantity_ordered*, unit_of_measure, unit_price*, tax_rate",
  goods_receipt_notes: "grn_number*, po_number*, vendor_gstin, receipt_date, line_number, item_code, description, quantity_received*, quantity_accepted, quantity_rejected, unit_of_measure",
};

async function download(path: string, filename: string) {
  const url = await fileObjectURL(path);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export default function Imports() {
  const [kind, setKind] = useState("purchase_orders");
  const [file, setFile] = useState<File | null>(null);
  const [res, setRes] = useState<string | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const isXlsx = file?.name.toLowerCase().endsWith(".xlsx");
  const isJson = file?.name.toLowerCase().endsWith(".json");

  return (
    <>
      <PageHeader title="Import procurement data"
        subtitle="Load vendors, purchase orders and goods receipts exported from your ERP or spreadsheets. Imports are all-or-nothing and re-check invoices that are waiting." />
      <div className="grid gap-5 lg:grid-cols-3">
        <Card title="Upload a file" className="lg:col-span-2">
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="File" hint="Excel workbook (.xlsx), CSV, or JSON.">
              <input type="file" accept=".xlsx,.csv,.json" className={inputCls} onChange={(e) => { setFile(e.target.files?.[0] ?? null); setRes(null); setErr(null); }} />
            </Field>
            <Field label="What the file contains"
              hint={isJson ? "Ignored for JSON: it can hold all three kinds." : isXlsx ? "Workbooks with sheets named Vendors / Purchase Orders / Goods Receipt Notes import all at once; this is used for a single unnamed sheet." : "One kind per CSV file."}>
              <select className={inputCls} value={kind} onChange={(e) => setKind(e.target.value)} disabled={isJson}>
                <option value="vendors">Vendors</option>
                <option value="purchase_orders">Purchase orders (one row per line item)</option>
                <option value="goods_receipt_notes">Goods receipts (one row per line item)</option>
              </select>
            </Field>
          </div>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <Button variant="primary" disabled={!file || busy} onClick={async () => {
              if (!file) return;
              setBusy(true); setErr(null); setRes(null); setWarnings([]);
              const fd = new FormData(); fd.append("file", file); fd.append("kind", kind);
              try {
                const r = await api<{ vendors: number; purchase_orders: number; goods_receipt_notes: number; warnings?: string[] }>("/imports/procurement", { method: "POST", body: fd });
                setRes(`Imported ${r.vendors} vendor(s), ${r.purchase_orders} PO(s) and ${r.goods_receipt_notes} GRN(s). Waiting invoices are being re-checked.`);
                setWarnings(r.warnings ?? []);
              } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
            }}>{busy ? "Importing…" : "Import"}</Button>
            {res && <span className="text-sm text-emerald-700">{res}</span>}
          </div>
          {warnings.length > 0 && (
            <ul className="mt-3 list-inside list-disc text-xs text-amber-800">{warnings.slice(0, 8).map((w, i) => <li key={i}>{w}</li>)}</ul>
          )}
          {err && <div className="mt-3 whitespace-pre-line"><ErrorBox error={err} /></div>}
        </Card>

        <Card title="Templates">
          <p className="text-sm text-zinc-600">Start from a template. Common alternative column names are also accepted (PO No, Qty, Rate, UOM, GST %, Received Qty…).</p>
          <div className="mt-3 flex flex-col gap-2">
            <Button onClick={() => download("/imports/template?format=xlsx", "procurement_import_template.xlsx")}>Excel template (all three sheets)</Button>
            <Button variant="ghost" onClick={() => download("/imports/template?format=csv&kind=vendors", "vendors_template.csv")}>Vendors CSV</Button>
            <Button variant="ghost" onClick={() => download("/imports/template?format=csv&kind=purchase_orders", "purchase_orders_template.csv")}>Purchase orders CSV</Button>
            <Button variant="ghost" onClick={() => download("/imports/template?format=csv&kind=goods_receipt_notes", "goods_receipt_notes_template.csv")}>Goods receipts CSV</Button>
          </div>
        </Card>

        <Card title="Columns" className="lg:col-span-3">
          <dl className="space-y-2 text-sm">
            {Object.entries(columns).map(([k, v]) => (
              <div key={k}><dt className="font-medium">{k.replace(/_/g, " ")}</dt><dd className="break-words text-xs text-zinc-600">{v}</dd></div>
            ))}
          </dl>
          <p className="mt-3 text-xs text-zinc-500">* required. One row per line item; repeat the PO/GRN columns on each of its rows. Dates as YYYY-MM-DD, DD/MM/YYYY or Excel date cells. If any row has an error, nothing is saved and every problem is listed with its row number.</p>
        </Card>
      </div>
    </>
  );
}
