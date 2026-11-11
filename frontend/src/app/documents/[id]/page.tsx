"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { fileObjectURL, post, put } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { Badge, Button, Card, ConfidenceDot, cx, ErrorBox, inputCls, Loading, PageHeader, Tabs } from "@/components/ui";
import { dateTime, docTypeLabel, docTypes, label } from "@/lib/format";

interface FieldV { value: string; confidence: number; page?: number; source: string }
interface ItemV {
  line_number: number; item_code: string; description: string; hsn_sac: string; quantity: number | null; unit: string; unit_price: number | null;
  discount: number | null; taxable_value: number | null; tax_rate: number | null; tax_amount: number | null; line_total: number | null;
  quantity_received?: number | null; quantity_accepted?: number | null; quantity_rejected?: number | null; confidence: number; source: string; raw?: string;
}
interface Data { document_type: string; fields: Record<string, FieldV>; items: ItemV[]; warnings: string[] | null }
interface Extraction {
  document_type: string;
  latest: { version: number; extraction_method: string; extraction_model: string | null; raw_text: string; extracted_data: Data; overall_confidence: number; ocr_mean_confidence: number | null; low_confidence_fields: string[]; created_by: string | null; created_at: string } | null;
  versions: { version: number; extraction_method: string; extraction_model: string | null; overall_confidence: number; created_at: string; created_by: string | null }[];
}
interface Doc {
  document_id: string; original_filename: string; mime_type: string; document_type: string; declared_document_type: string | null; classification_confidence: number | null;
  processing_status: string; processing_stage: string | null; processing_error: string | null; sha256_hash: string; file_size_bytes: number; page_count: number | null;
  received_at: string; uploaded_by: string | null; source_type: string;
  email_from: string | null; email_from_name: string | null; email_subject: string | null; email_sent_at: string | null; invoice_id: string | null; purchase_order_id: string | null; goods_receipt_note_id: string | null;
  duplicate_of_document_id: string | null;
  events: { event_type: string; event_description: string; created_at: string; actor_email: string | null }[];
}

const fieldSets: Record<string, string[]> = {
  INVOICE: ["invoice_number", "invoice_date", "due_date", "po_reference", "seller_name", "seller_gstin", "buyer_name", "buyer_gstin", "currency", "payment_terms",
    "subtotal", "taxable_amount", "cgst", "sgst", "igst", "total_tax", "other_charges", "round_off", "grand_total"],
  PO: ["po_number", "po_date", "vendor_name", "vendor_gstin", "buyer_name", "buyer_gstin", "currency", "total_amount"],
  GRN: ["grn_number", "receipt_date", "po_reference", "vendor_name", "vendor_gstin", "invoice_reference"],
};
const itemCols: Record<string, (keyof ItemV)[]> = {
  INVOICE: ["item_code", "description", "hsn_sac", "quantity", "unit", "unit_price", "discount", "taxable_value", "tax_rate", "tax_amount", "line_total"],
  PO: ["item_code", "description", "hsn_sac", "quantity", "unit", "unit_price", "tax_rate"],
  GRN: ["item_code", "description", "quantity_received", "quantity_accepted", "quantity_rejected", "unit"],
};
const textCols = new Set(["item_code", "description", "hsn_sac", "unit"]);

function Viewer({ doc }: { doc: Doc }) {
  const [url, setUrl] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    let u: string | null = null;
    fileObjectURL(`/documents/${doc.document_id}/file`).then((x) => { u = x; setUrl(x); }).catch((e) => setErr(String(e.message ?? e)));
    return () => { if (u) URL.revokeObjectURL(u); };
  }, [doc.document_id]);
  if (err) return <ErrorBox error={err} />;
  if (!url) return <Loading what="Loading original" />;
  return doc.mime_type === "application/pdf"
    ? <iframe src={url} title="Original document" className="h-[78vh] w-full rounded border border-zinc-200 dark:border-zinc-800" />
    : <img src={url} alt="Original document" className="w-full rounded border border-zinc-200 dark:border-zinc-800" />;
}

function Editor({ docId, ex, onSaved }: { docId: string; ex: Extraction; onSaved: () => void }) {
  const base = ex.latest?.extracted_data ?? { document_type: ex.document_type, fields: {}, items: [], warnings: [] };
  const [docType, setDocType] = useState(ex.document_type);
  const [fields, setFields] = useState<Record<string, FieldV>>(() => structuredClone(base.fields ?? {}));
  const [items, setItems] = useState<ItemV[]>(() => structuredClone(base.items ?? []));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const low = new Set(ex.latest?.low_confidence_fields ?? []);
  const fset = fieldSets[docType] ?? [];
  const cols = itemCols[docType] ?? itemCols.INVOICE;

  const setField = (k: string, v: string) => setFields((f) => ({ ...f, [k]: { ...(f[k] ?? { confidence: 1, source: "manual" }), value: v } }));
  const setCell = (i: number, k: keyof ItemV, v: string) => setItems((its) => its.map((it, j) => j !== i ? it : {
    ...it, [k]: textCols.has(k as string) ? v : v === "" ? null : Number(v.replace(/,/g, "")),
  }));
  const save = async () => {
    setBusy(true); setErr(null); setSaved(false);
    try {
      const clean = Object.fromEntries(Object.entries(fields).filter(([, f]) => f && f.value?.trim()));
      await put(`/documents/${docId}/extraction`, { document_type: docType, fields: clean, items, warnings: [] });
      setSaved(true);
      onSaved();
    } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  };
  return (
    <div className="space-y-4">
      {base.warnings && base.warnings.length > 0 && <div className="rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-950 dark:text-amber-200">{base.warnings.join(" · ")}</div>}
      <div className="flex items-center gap-2 text-sm">
        <span className="text-zinc-500">Document type</span>
        <select className={inputCls + " w-40"} value={docType} onChange={(e) => setDocType(e.target.value)}>
          {docTypes.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
      </div>
      {fset.length > 0 && (
        <div className="grid gap-x-4 gap-y-2 sm:grid-cols-2">
          {fset.map((k) => {
            const f = fields[k];
            const flagged = low.has(k);
            return (
              <label key={k} className="block">
                <span className="mb-0.5 flex items-center justify-between text-xs">
                  <span className={cx("font-medium", flagged ? "text-amber-700" : "text-zinc-600 dark:text-zinc-400")}>{label(k)}{flagged && " · check"}</span>
                  <span className="flex items-center gap-1">{f?.source && <span className="text-zinc-400">{f.source}{f.page ? ` p${f.page}` : ""}</span>}<ConfidenceDot value={f?.confidence} /></span>
                </span>
                <input className={cx(inputCls, flagged && "border-amber-400")} value={f?.value ?? ""} onChange={(e) => setField(k, e.target.value)}
                  placeholder={k.includes("date") ? "YYYY-MM-DD" : ""} />
              </label>
            );
          })}
        </div>
      )}
      {["INVOICE", "PO", "GRN"].includes(docType) && (
        <div>
          <div className="mb-1 flex items-center justify-between">
            <span className="text-sm font-medium">Line items</span>
            <Button variant="ghost" onClick={() => setItems((its) => [...its, { line_number: its.length + 1, item_code: "", description: "", hsn_sac: "", quantity: null, unit: "", unit_price: null, discount: null, taxable_value: null, tax_rate: null, tax_amount: null, line_total: null, confidence: 1, source: "manual" }])}>+ Add line</Button>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead><tr className="text-left text-zinc-500">{["#", ...cols.map((c) => label(c as string)), ""].map((h, i) => <th key={i} className="px-1 py-1 font-medium">{h}</th>)}</tr></thead>
              <tbody>
                {items.map((it, i) => (
                  <tr key={i} className={cx(it.confidence < 0.75 && "bg-amber-50 dark:bg-amber-950/40")} title={it.raw}>
                    <td className="px-1 text-zinc-500">{i + 1}</td>
                    {cols.map((c) => (
                      <td key={c as string} className="px-0.5 py-0.5">
                        <input className={cx(inputCls, "px-1.5 py-1 text-xs", c === "description" ? "min-w-48" : "min-w-16")}
                          value={(it[c] as string | number | null) ?? ""} onChange={(e) => setCell(i, c, e.target.value)} />
                      </td>
                    ))}
                    <td><button className="px-1 text-zinc-400 hover:text-rose-600" onClick={() => setItems((its) => its.filter((_, j) => j !== i))} title="Remove line">✕</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
      <ErrorBox error={err} />
      <div className="flex items-center gap-3">
        <Button variant="primary" disabled={busy} onClick={save}>{busy ? "Saving…" : "Save corrections & re-run checks"}</Button>
        {saved && <span className="text-sm text-emerald-600">Saved as a new version; downstream records re-evaluated.</span>}
      </div>
    </div>
  );
}

function Detail() {
  const { id } = useParams<{ id: string }>();
  const doc = useApi<Doc>(`/documents/${id}`, { pollMs: 2000, pollWhile: (d) => ["RECEIVED", "CLASSIFICATION_PENDING", "EXTRACTION_PENDING", "VALIDATION_PENDING"].includes(d.processing_status) });
  const ex = useApi<Extraction>(doc.data && doc.data.processing_status !== "CLASSIFICATION_PENDING" ? `/documents/${id}/extraction?v=${doc.data.processing_status}` : null);
  const [tab, setTab] = useState("fields");
  const [err, setErr] = useState<string | null>(null);
  const [rerunType, setRerunType] = useState("");
  if (doc.error) return <ErrorBox error={doc.error} />;
  const d = doc.data;
  if (!d) return <Loading />;
  const reprocess = async (t: string) => {
    setErr(null);
    try { await post(`/documents/${id}/process`, { document_type: t }); doc.reload(); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
  };
  return (
    <>
      <PageHeader title={d.original_filename}
        subtitle={<span className="flex flex-wrap items-center gap-2"><Badge tone="gray">{docTypeLabel(d.document_type)}</Badge><Badge value={d.processing_status} />
          {d.processing_stage && <span>{d.processing_stage}…</span>}
          <span>{d.page_count ?? "?"} page(s) · {(d.file_size_bytes / 1024).toFixed(0)} KB · {d.source_type} · {dateTime(d.received_at)}{d.uploaded_by ? ` by ${d.uploaded_by}` : ""}</span>
          {d.source_type === "EMAIL" && <span className="rounded bg-sky-50 px-2 py-0.5 text-sky-900 ring-1 ring-sky-200">✉ Received by email from <b>{d.email_from_name ? `${d.email_from_name} <${d.email_from}>` : d.email_from}</b>{d.email_subject ? <> · “{d.email_subject}”</> : null}</span>}</span>}
        actions={<>
          {d.invoice_id && <Link href={`/invoices/${d.invoice_id}`}><Button variant="primary">Open invoice →</Button></Link>}
          {d.purchase_order_id && <Link href={`/purchase-orders/${d.purchase_order_id}`}><Button>Open PO →</Button></Link>}
          {d.goods_receipt_note_id && <Link href={`/grns/${d.goods_receipt_note_id}`}><Button>Open GRN →</Button></Link>}
          <select className={inputCls + " w-48"} value={rerunType} onChange={(e) => setRerunType(e.target.value)} aria-label="Document type for re-run">
            <option value="">Detect type automatically</option>
            {docTypes.map((t) => <option key={t.value} value={t.value}>Treat as: {t.label}</option>)}
          </select>
          <Button onClick={() => reprocess(rerunType)}>Re-run extraction</Button>
        </>} />
      {(d.document_type === "OTHER" || (d.classification_confidence ?? 1) < 0.6) && d.processing_status !== "CLASSIFICATION_PENDING" && (
        <div className="mb-4 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200">
          The document type could not be determined reliably{d.classification_confidence !== null ? ` (${Math.round(d.classification_confidence * 100)}% confidence)` : ""}.
          Choose the correct type above and re-run extraction.
        </div>
      )}
      <ErrorBox error={err ?? d.processing_error} />
      <div className="grid gap-5 xl:grid-cols-2">
        <Card title="Original"><Viewer doc={d} /></Card>
        <div>
          <Tabs value={tab} onChange={setTab} tabs={[{ key: "fields", label: "Extracted data" }, { key: "text", label: "Raw text" }, { key: "history", label: "History" }]} />
          {!ex.data ? <Loading what="Waiting for extraction" /> : (
            <>
              {tab === "fields" && (
                <Card title={<span className="flex items-center gap-2">Version {ex.data.latest?.version ?? "—"} · {ex.data.latest?.extraction_method === "VLM" ? "Vision model" : ex.data.latest?.extraction_method}
                  {ex.data.latest?.extraction_model && <Badge tone="violet">{ex.data.latest.extraction_model.replace(/^vlm:/, "")}</Badge>} <ConfidenceDot value={ex.data.latest?.overall_confidence} /></span>}>
                  <Editor key={`${ex.data.latest?.version}-${ex.data.document_type}`} docId={id} ex={ex.data} onSaved={() => { doc.reload(); ex.reload(); }} />
                </Card>
              )}
              {tab === "text" && <Card title={`Extracted text${ex.data.latest?.ocr_mean_confidence && ex.data.latest.extraction_method === "OCR" ? ` (OCR ${Math.round(ex.data.latest.ocr_mean_confidence)}%)` : ""}`}>
                <pre className="max-h-[70vh] overflow-auto whitespace-pre text-xs">{ex.data.latest?.raw_text}</pre></Card>}
              {tab === "history" && (
                <Card title="Processing history">
                  <ul className="divide-y divide-zinc-100 text-sm dark:divide-zinc-800">
                    {d.events.map((e, i) => <li key={i} className="py-2"><span className="text-xs text-zinc-500">{dateTime(e.created_at)}</span> <Badge tone="gray">{e.event_type}</Badge> {e.event_description}{e.actor_email && <span className="text-xs text-zinc-500"> · {e.actor_email}</span>}</li>)}
                  </ul>
                  <h3 className="mt-4 text-sm font-medium">Extraction versions</h3>
                  <ul className="text-sm">{ex.data.versions.map((v) => <li key={v.version}>v{v.version} · {v.extraction_method}{v.extraction_model ? ` (${v.extraction_model.replace(/^vlm:/, "")})` : ""} · {Math.round(v.overall_confidence * 100)}% · {dateTime(v.created_at)}{v.created_by ? ` · ${v.created_by}` : ""}</li>)}</ul>
                  <p className="mt-3 break-all text-xs text-zinc-500">SHA-256 {d.sha256_hash}</p>
                </Card>
              )}
            </>
          )}
        </div>
      </div>
    </>
  );
}

export default function DocumentPage() {
  return <Suspense fallback={<Loading />}><Detail /></Suspense>;
}
