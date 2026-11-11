"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useRef, useState } from "react";
import { api, type Page } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { useAuth } from "@/lib/auth";
import { Badge, Button, Card, ConfidenceDot, Empty, ErrorBox, Field, inputCls, Loading, PageHeader, RowLink, Table, td } from "@/components/ui";
import { dateTime, docTypeLabel, docTypes, label } from "@/lib/format";

interface Doc {
  document_id: string; document_type: string; declared_document_type: string | null; classification_confidence: number | null; original_filename: string;
  file_size_bytes: number; duplicate_of_document_id: string | null; received_at: string; processing_status: string; processing_stage: string | null;
  processing_error: string | null; uploaded_by: string | null; source_type: string; email_from: string | null; email_subject: string | null; invoice_id: string | null; purchase_order_id: string | null; goods_receipt_note_id: string | null;
  extraction_confidence: number | null;
}

const busyStates = ["RECEIVED", "CLASSIFICATION_PENDING", "EXTRACTION_PENDING", "VALIDATION_PENDING", "PROCESSING"];

function Upload({ onDone }: { onDone: () => void }) {
  const { can } = useAuth();
  const allowedTypes = docTypes.filter((t) => can("AP_MANAGER") || (t.value !== "PO" && t.value !== "GRN"));
  const [type, setType] = useState("");
  const [busy, setBusy] = useState(false);
  const [msgs, setMsgs] = useState<string[]>([]);
  const [drag, setDrag] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const send = async (files: FileList | null) => {
    if (!files?.length) return;
    setBusy(true);
    const out: string[] = [];
    for (const f of Array.from(files)) {
      const fd = new FormData();
      fd.append("file", f);
      if (type) fd.append("document_type", type);
      try {
        const r = await api<{ duplicate_of_document_id?: string }>("/documents", { method: "POST", body: fd });
        out.push(`✓ ${f.name}${r.duplicate_of_document_id ? " — identical to an earlier upload (will be checked as a duplicate)" : ""}`);
      } catch (e) {
        out.push(`✕ ${f.name}: ${e instanceof Error ? e.message : e}`);
      }
    }
    setMsgs(out);
    setBusy(false);
    if (input.current) input.current.value = "";
    onDone();
  };
  return (
    <Card title="Upload documents">
      <div className="grid gap-4 md:grid-cols-[1fr_14rem]">
        <div onDragOver={(e) => { e.preventDefault(); setDrag(true); }} onDragLeave={() => setDrag(false)}
          onDrop={(e) => { e.preventDefault(); setDrag(false); send(e.dataTransfer.files); }}
          onClick={() => input.current?.click()}
          className={`flex cursor-pointer flex-col items-center justify-center rounded-md border-2 border-dashed p-6 text-center text-sm ${drag ? "border-sky-500 bg-sky-50 dark:bg-sky-950" : "border-zinc-300 dark:border-zinc-700"}`}>
          <span className="font-medium">{busy ? "Uploading…" : "Drop PDFs or images here, or click to choose"}</span>
          <span className="mt-1 text-xs text-zinc-500">Invoices, purchase orders, goods receipts · PDF, PNG, JPEG, TIFF</span>
          <input ref={input} type="file" multiple accept=".pdf,.png,.jpg,.jpeg,.tif,.tiff,application/pdf,image/*" className="hidden" onChange={(e) => send(e.target.files)} />
        </div>
        <Field label="Document type" hint="Choose it when you know it: the system only overrides your choice if the document's title clearly says otherwise.">
          <select className={inputCls} value={type} onChange={(e) => setType(e.target.value)}>
            <option value="">Detect automatically</option>
            {allowedTypes.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
          </select>
          {!can("AP_MANAGER") && <span className="mt-1 block text-xs text-zinc-500">Purchase orders and goods receipts are master data: managers import or upload them.</span>}
        </Field>
      </div>
      {msgs.length > 0 && <ul className="mt-3 space-y-0.5 text-sm">{msgs.map((m, i) => <li key={i} className={m.startsWith("✕") ? "text-rose-600" : ""}>{m}</li>)}</ul>}
    </Card>
  );
}

function Docs() {
  const sp = useSearchParams();
  const router = useRouter();
  const status = sp.get("status") ?? "";
  const type = sp.get("type") ?? "";
  const { data, error, reload } = useApi<Page<Doc>>(`/documents?status=${status}&type=${type}&limit=100`, {
    pollMs: 2000, pollWhile: (d) => d.items.some((x) => busyStates.includes(x.processing_status)),
  });
  const set = (k: string, v: string) => { const p = new URLSearchParams(sp.toString()); if (v) p.set(k, v); else p.delete(k); router.replace(`/documents?${p}`); };
  return (
    <>
      <PageHeader title="Documents" subtitle="Every uploaded original, its classification and processing stage." />
      <Upload onDone={reload} />
      <div className="my-4 flex flex-wrap gap-2">
        <select className={inputCls + " w-48"} value={status} onChange={(e) => set("status", e.target.value)}>
          <option value="">All statuses</option>
          {["CLASSIFICATION_PENDING", "EXTRACTION_PENDING", "EXTRACTION_REVIEW", "VALIDATION_PENDING", "UNMATCHED", "MATCHED", "PROCESSED", "FAILED"].map((s) => <option key={s} value={s}>{label(s)}</option>)}
        </select>
        <select className={inputCls + " w-40"} value={type} onChange={(e) => set("type", e.target.value)}>
          <option value="">All types</option>
          {docTypes.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
      </div>
      <ErrorBox error={error} />
      {!data ? <Loading /> : data.items.length === 0 ? <Empty>No documents yet. Upload one above.</Empty> : (
        <Card>
          <Table head={["File", "Type", "Status", "Extraction", "Record", "Received", "By"]}>
            {data.items.map((d) => (
              <tr key={d.document_id}>
                <td className={td + " max-w-72"}><RowLink href={`/documents/${d.document_id}`}>{d.original_filename}</RowLink>
                  {d.duplicate_of_document_id && <div className="text-xs text-rose-600">identical file uploaded before</div>}
                  {d.processing_error && <div className="text-xs text-rose-600">{d.processing_error}</div>}</td>
                <td className={td}><Badge tone="gray">{docTypeLabel(d.document_type)}</Badge>{d.classification_confidence !== null && <div className="text-xs text-zinc-500">{Math.round(d.classification_confidence * 100)}%</div>}</td>
                <td className={td}><Badge value={d.processing_status} />{d.processing_stage && <div className="text-xs text-zinc-500">{d.processing_stage}…</div>}</td>
                <td className={td}><ConfidenceDot value={d.extraction_confidence} /></td>
                <td className={td}>
                  {d.invoice_id && <RowLink href={`/invoices/${d.invoice_id}`}>Invoice →</RowLink>}
                  {d.purchase_order_id && <RowLink href={`/purchase-orders/${d.purchase_order_id}`}>PO →</RowLink>}
                  {d.goods_receipt_note_id && <RowLink href={`/grns/${d.goods_receipt_note_id}`}>GRN →</RowLink>}
                </td>
                <td className={td + " whitespace-nowrap text-xs"}>{dateTime(d.received_at)}</td>
                <td className={td + " text-xs"}>{d.source_type === "EMAIL" ? <span title={d.email_subject ?? ""}>✉ {d.email_from}</span> : d.uploaded_by}</td>
              </tr>
            ))}
          </Table>
          <Button variant="ghost" className="mt-2" onClick={reload}>Refresh</Button>
        </Card>
      )}
    </>
  );
}

export default function DocumentsPage() {
  return <Suspense fallback={<Loading />}><Docs /></Suspense>;
}
