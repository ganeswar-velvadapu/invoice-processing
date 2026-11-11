"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { Suspense, useState } from "react";
import { useApi } from "@/lib/hooks";
import { post, type Page } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Badge, Button, Card, ConfidenceDot, cx, ErrorBox, Field, inputCls, Loading, PageHeader, ReasonDialog, RowLink, Table, Tabs, td } from "@/components/ui";
import { date, dateTime, label, money, num, pct } from "@/lib/format";

interface Check { check: string; status: string; expected?: string; actual?: string; reason: string; waived?: boolean }
interface Decision {
  decision: string; summary: string; recommended_action: string; requires_human_review: boolean; deciding_class?: string;
  deciding_checks?: string[]; checks: Check[]; matched_purchase_orders: string[] | null; rule_version: string; tolerance_version: number;
}
interface POMatch { po_document_id: string | null; po_source: string; purchase_order_id: string; po_number: string; po_date: string; po_status: string; match_method: string; match_status: string; match_confidence: number; evidence: string[]; matched_by: string | null; total_amount: number }
interface Item { invoice_item_id: string; line_number: number; item_code: string; description: string; hsn_sac: string; quantity: number | null; unit_of_measure: string; unit_price: number | null; discount: number; taxable_value: number | null; tax_rate: number | null; tax_amount: number | null; line_total: number | null; confidence: number | null }
interface Invoice {
  invoice_id: string; document_id: string; invoice_number: string | null; invoice_date: string | null; due_date: string | null; financial_year: string | null;
  vendor_id: string | null; vendor_name: string | null; vendor_approved: boolean | null; seller_name: string | null; seller_gstin: string | null; buyer_name: string | null; buyer_gstin: string | null;
  po_references: string[]; currency: string; payment_terms: string | null; subtotal: number | null; taxable_amount: number | null; cgst_amount: number | null; sgst_amount: number | null;
  igst_amount: number | null; other_charges: number | null; round_off: number | null; total_tax: number | null; grand_total: number | null; extraction_confidence: number | null;
  invoice_status: string; decision: string | null; decision_summary: string | null; decision_detail: Decision | null; duplicate_of_invoice_id: string | null; duplicate_of_invoice_number: string | null;
  original_filename: string; last_verified_at: string | null;
  return_note: string | null; returned_at: string | null; returned_by: string | null; assigned_to: string | null; items: Item[]; po_matches: POMatch[];
  goods_receipts: { goods_receipt_note_id: string; grn_number: string; receipt_date: string; document_id: string | null; source: string }[];
  submitted_by: string | null; submitted_at: string | null; clerk_recommendation: "APPROVE" | "REJECT" | null; clerk_note: string | null;
  low_confidence_fields: string[] | null;
}
interface Exc { invoice_exception_id: string; exception_code: string; severity: string; description: string; exception_status: string; resolution_note: string | null; resolved_by: string | null; created_at: string; resolved_at: string | null }
interface CmpRow extends Item {
  po_lines: { po_number: string; purchase_order_id: string; po_line: number; item_code: string; description: string; quantity_ordered: number; unit_price: number; tax_rate: number;
    allocated_quantity: number; match_basis: string; quantity_received: number; quantity_accepted: number; quantity_invoiced_other: number;
    receipts: { grn_number: string; receipt_date: string; received: number; accepted: number; rejected: number }[] }[];
}
interface Ev { event_type: string; event_description: string; created_at: string; actor_type: string; actor_email: string | null }

const statusIcon: Record<string, string> = { PASS: "✓", FAIL: "✕", WARNING: "!", NOT_RUN: "–" };

function Checks({ checks }: { checks: Check[] }) {
  const order: Record<string, number> = { FAIL: 0, WARNING: 1, NOT_RUN: 2, PASS: 3 };
  const sorted = [...checks].sort((a, b) => order[a.status] - order[b.status]);
  return (
    <ul className="divide-y divide-zinc-100 dark:divide-zinc-800">
      {sorted.map((c, i) => (
        <li key={i} className={cx("flex gap-3 py-2 text-sm", c.waived && "opacity-60")}>
          <span className={cx("mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-xs font-bold",
            c.status === "PASS" ? "bg-emerald-100 text-emerald-700" : c.status === "FAIL" ? "bg-rose-100 text-rose-700" : "bg-amber-100 text-amber-800")}>{statusIcon[c.status]}</span>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{label(c.check)}</span>
              {c.waived && <Badge value="WAIVED" />}
              {(c.expected || c.actual) && c.status !== "PASS" && (
                <span className="text-xs text-zinc-500">expected <b className="text-zinc-700 dark:text-zinc-300">{c.expected || "—"}</b> · actual <b className="text-zinc-700 dark:text-zinc-300">{c.actual || "—"}</b></span>
              )}
            </div>
            <p className="text-zinc-600 dark:text-zinc-400">{c.reason}</p>
          </div>
        </li>
      ))}
    </ul>
  );
}

function Comparison({ rows, currency }: { rows: CmpRow[]; currency: string }) {
  return (
    <Table head={["#", "Invoice line", "Qty", "Price", "GST", "PO line", "Ordered", "PO price", "PO GST", "Accepted (GRN)", "Other invoices"]}>
      {rows.map((r) => {
        const pls = r.po_lines.length ? r.po_lines : [null];
        return pls.map((pl, k) => {
          const priceOff = pl && r.unit_price !== null && Math.abs(r.unit_price - pl.unit_price) > 0.005;
          const taxOff = pl && r.tax_rate !== null && pl.tax_rate > 0 && Math.abs(r.tax_rate - pl.tax_rate) > 0.01;
          const qtyOff = pl && pl.allocated_quantity + pl.quantity_invoiced_other > pl.quantity_accepted + 1e-9;
          const ordOff = pl && pl.allocated_quantity + pl.quantity_invoiced_other > pl.quantity_ordered + 1e-9;
          return (
            <tr key={r.invoice_item_id + k}>
              {k === 0 && <>
                <td className={td} rowSpan={pls.length}>{r.line_number}</td>
                <td className={td + " min-w-40 max-w-56"} rowSpan={pls.length}><div className="whitespace-nowrap font-medium">{r.item_code}</div><div className="text-xs text-zinc-500">{r.description}</div></td>
                <td className={td + " text-right"} rowSpan={pls.length}>{num(r.quantity)} <span className="text-xs text-zinc-500">{r.unit_of_measure}</span></td>
                <td className={cx(td, "text-right", priceOff && "font-semibold text-rose-600")} rowSpan={pls.length}>{money(r.unit_price, currency)}</td>
                <td className={cx(td, "text-right", taxOff && "font-semibold text-rose-600")} rowSpan={pls.length}>{r.tax_rate !== null ? `${r.tax_rate}%` : "—"}</td>
              </>}
              {pl ? <>
                <td className={td + " min-w-44"}><span className="whitespace-nowrap"><RowLink href={`/purchase-orders/${pl.purchase_order_id}`}>{pl.po_number}</RowLink> <span className="text-xs text-zinc-500">#{pl.po_line} · {num(pl.allocated_quantity)} alloc.</span></span><div className="text-xs text-zinc-500">{pl.match_basis}</div></td>
                <td className={cx(td, "text-right", ordOff && "font-semibold text-rose-600")}>{num(pl.quantity_ordered)}</td>
                <td className={td + " text-right"}>{money(pl.unit_price, currency)}</td>
                <td className={td + " text-right"}>{pl.tax_rate ? `${pl.tax_rate}%` : "—"}</td>
                <td className={cx(td, "text-right", qtyOff && "font-semibold text-rose-600")} title={pl.receipts.map((g) => `${g.grn_number}: ${g.accepted} accepted / ${g.received} received`).join("\n")}>
                  {pl.receipts.length ? num(pl.quantity_accepted) : <span className="text-sky-600">not received</span>}
                  {pl.receipts.length > 0 && <div className="text-xs text-zinc-500">{pl.receipts.map((g) => g.grn_number).join(", ")}</div>}
                </td>
                <td className={td + " text-right"}>{num(pl.quantity_invoiced_other)}</td>
              </> : <td className={td + " text-rose-600"} colSpan={6}>Not matched to any PO line</td>}
            </tr>
          );
        });
      })}
    </Table>
  );
}

function ExceptionRow({ e, onDone }: { e: Exc; onDone: () => void }) {
  const { can } = useAuth();
  const [dlg, setDlg] = useState<"CHECK" | "RESOLVE" | "WAIVE" | null>(null);
  const act = async (action: string, note = "") => { await post(`/exceptions/${e.invoice_exception_id}`, { action, note }); onDone(); };
  const open = e.exception_status === "OPEN" || e.exception_status === "ASSIGNED";
  return (
    <li className="py-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge value={e.severity} /><span className="font-medium">{label(e.exception_code)}</span><Badge value={e.exception_status} />
        <span className="text-xs text-zinc-500">{dateTime(e.created_at)}</span>
        <div className="ml-auto flex gap-2">
          {open && <Button variant="primary" onClick={() => setDlg("CHECK")}>✓ Mark as checked</Button>}
          {open && <Button onClick={() => setDlg("RESOLVE")}>Resolve</Button>}
          {open && can("AP_MANAGER") && <Button onClick={() => setDlg("WAIVE")}>Waive</Button>}
          {!open && e.exception_status !== "RESOLVED" && <Button variant="ghost" onClick={() => act("REOPEN")}>Reopen</Button>}
        </div>
      </div>
      <p className="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{e.description}</p>
      {e.resolution_note && (
        <p className={cx("mt-1 text-sm", e.exception_status === "CHECKED" ? "rounded bg-emerald-50 px-2 py-1 text-emerald-900" : "text-xs text-zinc-500")}>
          {e.exception_status === "CHECKED" ? "✓ Checked" : "Note"}{e.resolved_by ? ` by ${e.resolved_by}` : ""}: {e.resolution_note}
        </p>
      )}
      <ReasonDialog open={dlg === "CHECK"} title={`Mark ${label(e.exception_code)} as checked`} label="What did you verify, and what is the explanation?" confirm="Mark as checked"
        extra={<p className="text-xs text-zinc-500">For issues you have verified against the PO, GRN and original but that are still true (e.g. supplier confirmed, credit note to follow). The manager sees your note when deciding.</p>}
        onClose={() => setDlg(null)} onSubmit={(n) => act("CHECK", n)} />
      <ReasonDialog open={dlg === "RESOLVE"} title={`Resolve ${label(e.exception_code)}`} label="Resolution note" confirm="Resolve"
        extra={<p className="text-xs text-zinc-500">If the underlying check still fails on re-verification, the exception is reopened automatically.</p>}
        onClose={() => setDlg(null)} onSubmit={(n) => act("RESOLVE", n)} />
      <ReasonDialog open={dlg === "WAIVE"} title={`Waive ${label(e.exception_code)}`} label="Why is this acceptable?" confirm="Waive"
        extra={<p className="text-xs text-zinc-500">A waived exception no longer blocks the decision. This is recorded in the audit trail.</p>}
        onClose={() => setDlg(null)} onSubmit={(n) => act("WAIVE", n)} />
    </li>
  );
}

function POMatches({ inv, onDone }: { inv: Invoice; onDone: () => void }) {
  const [pick, setPick] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const { data: pos } = useApi<Page<{ purchase_order_id: string; po_number: string; po_date: string; total_amount: number; po_status: string }>>(
    inv.vendor_id ? `/purchase-orders?vendor_id=${inv.vendor_id}&limit=100` : null);
  const act = async (purchase_order_id: string, action: string) => {
    setErr(null);
    try { await post(`/invoices/${inv.invoice_id}/po-match`, { purchase_order_id, action, note: "selected in AP review" }); onDone(); }
    catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
  };
  const hasManual = inv.po_matches.some((m) => m.match_method === "MANUAL");
  return (
    <Card title="Purchase order match" actions={hasManual && <Button variant="ghost" onClick={() => act("", "CLEAR")}>Clear manual choice</Button>}>
      <ErrorBox error={err} />
      {inv.po_matches.length === 0 ? <p className="text-sm text-zinc-500">No PO matched or proposed. Invoice references: {inv.po_references.join(", ") || "none"}.</p> : (
        <ul className="space-y-2">
          {inv.po_matches.map((m) => (
            <li key={m.purchase_order_id} className="rounded-md border border-zinc-200 p-3 text-sm dark:border-zinc-800">
              <div className="flex flex-wrap items-center gap-2">
                <RowLink href={`/purchase-orders/${m.purchase_order_id}`}>{m.po_number}</RowLink>
                <Badge value={m.match_status} /><Badge tone="gray">{m.match_method.replace(/_/g, " ")}</Badge>
                <span className="text-xs text-zinc-500">confidence {pct(m.match_confidence)} · {date(m.po_date)} · {m.po_status}{m.matched_by ? ` · by ${m.matched_by}` : ""}</span>
                <div className="ml-auto flex gap-2">
                  {m.match_status !== "CONFIRMED" && <Button onClick={() => act(m.purchase_order_id, "CONFIRM")}>Confirm</Button>}
                  {m.match_status !== "REJECTED" && <Button variant="ghost" onClick={() => act(m.purchase_order_id, "REJECT")}>Not this PO</Button>}
                </div>
              </div>
              {m.evidence?.length > 0 && <ul className="mt-1 list-inside list-disc text-xs text-zinc-500">{m.evidence.map((e, i) => <li key={i}>{e}</li>)}</ul>}
            </li>
          ))}
        </ul>
      )}
      {pos && pos.items.length > 0 && (
        <div className="mt-3 flex items-end gap-2">
          <Field label="Choose a different PO of this vendor">
            <select className={inputCls} value={pick} onChange={(e) => setPick(e.target.value)}>
              <option value="">Select…</option>
              {pos.items.map((p) => <option key={p.purchase_order_id} value={p.purchase_order_id}>{p.po_number} · {date(p.po_date)} · {money(p.total_amount)} · {p.po_status}</option>)}
            </select>
          </Field>
          <Button disabled={!pick} onClick={() => act(pick, "CONFIRM")}>Use this PO</Button>
        </div>
      )}
    </Card>
  );
}

function Detail() {
  const { id } = useParams<{ id: string }>();
  const { can } = useAuth();
  const inv = useApi<Invoice>(`/invoices/${id}`);
  const exc = useApi<Exc[]>(`/invoices/${id}/exceptions`);
  const cmp = useApi<CmpRow[]>(`/invoices/${id}/comparison`);
  const ev = useApi<Ev[]>(`/invoices/${id}/events`);
  const [tabChoice, setTab] = useState<string | null>(null);
  const [dlg, setDlg] = useState<"reject" | "override" | "return" | "resubmit" | "submit" | "approve" | null>(null);
  const [recommend, setRecommend] = useState<"APPROVE" | "REJECT">("APPROVE");
  const [target, setTarget] = useState("APPROVED");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const reloadAll = () => { inv.reload(); exc.reload(); cmp.reload(); ev.reload(); };
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true); setErr(null);
    try { await fn(); reloadAll(); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  };
  if (inv.error) return <ErrorBox error={inv.error} />;
  const i = inv.data;
  if (!i) return <Loading />;
  const d = i.decision_detail;
  const final = ["APPROVED", "REJECTED", "PAID", "PAYMENT_PENDING"].includes(i.invoice_status);
  const openExc = (exc.data ?? []).filter((e) => e.exception_status === "OPEN" || e.exception_status === "ASSIGNED");
  const checkedExc = (exc.data ?? []).filter((e) => e.exception_status === "CHECKED");
  const needsPerson = ["ON_HOLD", "REVIEW_REQUIRED", "AWAITING_APPROVAL", "RETURNED"].includes(i.invoice_status);
  const tab = tabChoice ?? (needsPerson && i.po_matches.length > 0 ? "lines" : "decision");
  const canSend = ["ON_HOLD", "REVIEW_REQUIRED", "READY_FOR_VERIFICATION", "RETURNED", "PENDING_PO_MATCH", "PENDING_GRN", "PENDING_DOCUMENTS"].includes(i.invoice_status);
  const approveAgainstSystem = i.decision !== "APPROVE";
  const canApprove = (i.decision === "APPROVE" && ["READY_FOR_VERIFICATION", "AWAITING_APPROVAL"].includes(i.invoice_status)) || i.invoice_status === "AWAITING_APPROVAL";

  return (
    <>
      <PageHeader
        title={<span className="flex flex-wrap items-center gap-2">Invoice {i.invoice_number ?? "(no number)"} <Badge value={i.invoice_status} /></span>}
        subtitle={<>{i.vendor_name ?? i.seller_name ?? "Unknown supplier"} · {date(i.invoice_date)} · <b>{money(i.grand_total, i.currency)}</b> · <Link className="text-sky-700 hover:underline dark:text-sky-400" href={`/documents/${i.document_id}`}>{i.original_filename}</Link></>}
        actions={<>
          <Button disabled={busy} onClick={() => run(() => post(`/invoices/${id}/verify`))}>Re-verify</Button>
          {canSend && <Button variant={can("AP_MANAGER") ? "secondary" : "primary"} disabled={busy} onClick={() => setDlg("submit")}
            title={openExc.length ? `Check the ${openExc.length} open exception(s) first to recommend approval` : ""}>Send for approval</Button>}
          {can("AP_MANAGER") && !final && <Button variant="primary" disabled={busy || !canApprove}
            title={!canApprove ? "Approve directly only when the system approved it, or once a clerk has sent it for approval" : ""}
            onClick={() => approveAgainstSystem ? setDlg("approve") : run(() => post(`/invoices/${id}/approve`, { note: "" }))}>Approve</Button>}
          {can("AP_MANAGER") && !final && <Button variant="danger" disabled={busy} onClick={() => setDlg("reject")}>Reject</Button>}
          {can("AP_MANAGER") && !final && i.invoice_status !== "RETURNED" && <Button disabled={busy} onClick={() => setDlg("return")}>Return to clerk</Button>}
          {can("AP_MANAGER") && <Button disabled={busy} onClick={() => setDlg("override")}>Override…</Button>}
        </>} />
      {i.invoice_status === "RETURNED" && (
        <div className="mb-4 rounded-md border border-violet-200 bg-violet-50 px-4 py-3 text-sm text-violet-900">
          <div className="font-medium">Returned to clerk{i.returned_by ? ` by ${i.returned_by}` : ""}{i.returned_at ? ` · ${dateTime(i.returned_at)}` : ""}{i.assigned_to ? ` · assigned to ${i.assigned_to}` : ""}</div>
          <p className="mt-1">{i.return_note}</p>
          <p className="mt-1 text-xs text-violet-700">Fix what the note asks (correct the extraction, check exceptions against the PO and GRN, confirm the PO), then use Send for approval.</p>
        </div>
      )}
      {i.invoice_status === "AWAITING_APPROVAL" && (
        <div className={cx("mb-4 rounded-md border px-4 py-3 text-sm", i.clerk_recommendation === "REJECT" ? "border-rose-200 bg-rose-50 text-rose-900" : "border-emerald-200 bg-emerald-50 text-emerald-900")}>
          <div className="font-medium">Awaiting manager approval · clerk recommends <b>{i.clerk_recommendation === "REJECT" ? "REJECT" : "APPROVE"}</b>
            {i.submitted_by ? ` · ${i.submitted_by}` : ""}{i.submitted_at ? ` · ${dateTime(i.submitted_at)}` : ""}</div>
          <p className="mt-1">{i.clerk_note}</p>
          {checkedExc.length > 0 && (
            <ul className="mt-2 space-y-0.5 text-xs">{checkedExc.map((e) => <li key={e.invoice_exception_id}>✓ <b>{label(e.exception_code)}</b>: {e.resolution_note}{e.resolved_by ? ` (${e.resolved_by})` : ""}</li>)}</ul>
          )}
          {approveAgainstSystem && <p className="mt-2 text-xs opacity-80">The system decision is {i.decision}: approving records an approval against the system with your reason.</p>}
        </div>
      )}
      <ErrorBox error={err} />
      {i.duplicate_of_invoice_id && (
        <div className="mb-4 rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-800 dark:border-rose-900 dark:bg-rose-950 dark:text-rose-200">
          Suspected duplicate of <Link className="underline" href={`/invoices/${i.duplicate_of_invoice_id}`}>{i.duplicate_of_invoice_number ?? "an earlier invoice"}</Link>.
        </div>
      )}
      {d && (
        <div className={cx("mb-5 rounded-lg border p-4", d.decision === "APPROVE" ? "border-emerald-200 bg-emerald-50/60 dark:border-emerald-900 dark:bg-emerald-950/30"
          : d.decision === "HOLD" || d.decision === "REJECT" ? "border-rose-200 bg-rose-50/60 dark:border-rose-900 dark:bg-rose-950/30"
          : d.decision === "REVIEW" ? "border-amber-200 bg-amber-50/60 dark:border-amber-900 dark:bg-amber-950/30" : "border-sky-200 bg-sky-50/60 dark:border-sky-900 dark:bg-sky-950/30")}>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs font-medium uppercase tracking-wide text-zinc-500">System decision</span><Badge value={d.decision} />
            {d.matched_purchase_orders?.length ? <span className="text-xs text-zinc-500">matched {d.matched_purchase_orders.join(", ")}</span> : null}
            <span className="ml-auto text-xs text-zinc-500">rules {d.rule_version} · tolerance v{d.tolerance_version} · verified {dateTime(i.last_verified_at)}</span>
          </div>
          <p className="mt-2 font-medium">{d.summary}</p>
          <p className="mt-1 text-sm text-zinc-700 dark:text-zinc-300"><span className="text-zinc-500">Recommended action:</span> {d.recommended_action}</p>
        </div>
      )}
      <Tabs value={tab} onChange={setTab} tabs={[
        { key: "decision", label: `Checks (${d?.checks.filter((c) => c.status !== "PASS").length ?? 0} issues)` },
        { key: "lines", label: "Invoice vs PO vs GRN" },
        { key: "exceptions", label: `Exceptions (${openExc.length} open${checkedExc.length ? `, ${checkedExc.length} checked` : ""})` },
        { key: "details", label: "Extracted details" },
        { key: "audit", label: "Audit trail" },
      ]} />
      {tab === "decision" && (
        <div className="grid gap-5 lg:grid-cols-3">
          <Card title="Checks performed" className="lg:col-span-2">{d ? <Checks checks={d.checks} /> : <p className="text-sm text-zinc-500">Not verified yet.</p>}</Card>
          <div className="space-y-5">
            <POMatches inv={i} onDone={reloadAll} />
            <Card title="Goods receipts">
              {i.goods_receipts.length === 0 ? <p className="text-sm text-zinc-500">None linked.</p> :
                <ul className="text-sm">{i.goods_receipts.map((g) => <li key={g.goods_receipt_note_id}><RowLink href={`/grns/${g.goods_receipt_note_id}`}>{g.grn_number}</RowLink> <span className="text-zinc-500">· {date(g.receipt_date)}</span></li>)}</ul>}
            </Card>
          </div>
        </div>
      )}
      {tab === "lines" && (
        <div className="space-y-5">
          <Card title="Related documents">
            <div className="grid gap-3 text-sm sm:grid-cols-3">
              <div className="rounded-md border border-zinc-200 p-3">
                <div className="text-xs font-medium uppercase tracking-wide text-zinc-500">Invoice</div>
                <div className="mt-1 font-medium">{i.invoice_number}</div>
                <div className="text-xs text-zinc-500">{date(i.invoice_date)} · {money(i.grand_total, i.currency)}</div>
                <RowLink href={`/documents/${i.document_id}`}>Open original →</RowLink>
              </div>
              <div className="rounded-md border border-zinc-200 p-3">
                <div className="text-xs font-medium uppercase tracking-wide text-zinc-500">Purchase order</div>
                {i.po_matches.filter((m) => m.match_status !== "REJECTED").length === 0 ? <p className="mt-1 text-zinc-500">No PO matched</p> :
                  i.po_matches.filter((m) => m.match_status !== "REJECTED").map((m) => (
                    <div key={m.purchase_order_id} className="mt-1">
                      <RowLink href={`/purchase-orders/${m.purchase_order_id}`}>{m.po_number}</RowLink> <Badge value={m.match_status} />
                      <div className="text-xs text-zinc-500">{date(m.po_date)} · {money(m.total_amount, i.currency)} · {m.po_source === "IMPORT" ? "from ERP import" : "from uploaded PDF"}</div>
                      {m.po_document_id && <RowLink href={`/documents/${m.po_document_id}`}>Open original →</RowLink>}
                    </div>
                  ))}
              </div>
              <div className="rounded-md border border-zinc-200 p-3">
                <div className="text-xs font-medium uppercase tracking-wide text-zinc-500">Goods receipts</div>
                {i.goods_receipts.length === 0 ? <p className="mt-1 text-zinc-500">No GRN yet</p> : i.goods_receipts.map((g) => (
                  <div key={g.goods_receipt_note_id} className="mt-1">
                    <RowLink href={`/grns/${g.goods_receipt_note_id}`}>{g.grn_number}</RowLink>
                    <div className="text-xs text-zinc-500">{date(g.receipt_date)} · {g.source === "IMPORT" ? "from ERP import" : "from uploaded PDF"}</div>
                    {g.document_id && <RowLink href={`/documents/${g.document_id}`}>Open original →</RowLink>}
                  </div>
                ))}
              </div>
            </div>
          </Card>
          <Card title="Line-level reconciliation (red = differs from PO or GRN)">{cmp.data ? <Comparison rows={cmp.data} currency={i.currency} /> : <Loading />}</Card>
        </div>
      )}
      {tab === "exceptions" && (
        <Card title="Exceptions">
          {!exc.data ? <Loading /> : exc.data.length === 0 ? <p className="text-sm text-zinc-500">No exceptions.</p> :
            <ul className="divide-y divide-zinc-100 dark:divide-zinc-800">{exc.data.map((e) => <ExceptionRow key={e.invoice_exception_id} e={e} onDone={reloadAll} />)}</ul>}
        </Card>
      )}
      {tab === "details" && (
        <div className="grid gap-5 lg:grid-cols-3">
          <Card title="Header" actions={<Link href={`/documents/${i.document_id}`} className="text-sm text-sky-700 hover:underline dark:text-sky-400">Correct extraction →</Link>}>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
              {([["Supplier", i.seller_name], ["Supplier GSTIN", i.seller_gstin], ["Vendor record", i.vendor_name ? `${i.vendor_name}${i.vendor_approved ? "" : " (not approved)"}` : "not found"],
                ["Buyer", i.buyer_name], ["Buyer GSTIN", i.buyer_gstin], ["Invoice date", date(i.invoice_date)], ["Due date", date(i.due_date)], ["Financial year", i.financial_year],
                ["PO reference(s)", i.po_references.join(", ")], ["Payment terms", i.payment_terms]] as [string, string | null][]).map(([k, v]) => (
                <div key={k} className="contents"><dt className="text-zinc-500">{k}</dt><dd className="break-all">{v || "—"}</dd></div>
              ))}
              <dt className="text-zinc-500">Extraction</dt><dd><ConfidenceDot value={i.extraction_confidence} />{i.low_confidence_fields?.length ? <span className="ml-2 text-xs text-amber-700">review: {i.low_confidence_fields.join(", ")}</span> : null}</dd>
            </dl>
          </Card>
          <Card title="Totals">
            <dl className="grid grid-cols-[1fr_auto] gap-y-1.5 text-sm">
              {([["Subtotal", i.subtotal], ["Taxable amount", i.taxable_amount], ["CGST", i.cgst_amount], ["SGST", i.sgst_amount], ["IGST", i.igst_amount], ["Total tax", i.total_tax],
                ["Other charges", i.other_charges], ["Round off", i.round_off]] as [string, number | null][]).map(([k, v]) => (
                <div key={k} className="contents"><dt className="text-zinc-500">{k}</dt><dd className="text-right">{money(v, i.currency)}</dd></div>
              ))}
              <dt className="font-semibold">Grand total</dt><dd className="text-right font-semibold">{money(i.grand_total, i.currency)}</dd>
            </dl>
          </Card>
          <Card title="Line items" className="lg:col-span-3">
            <Table head={["#", "Code", "Description", "HSN", "Qty", "Unit", "Price", "Taxable", "GST %", "Tax", "Total", "Conf."]}>
              {i.items.map((it) => (
                <tr key={it.invoice_item_id}>
                  <td className={td}>{it.line_number}</td><td className={td}>{it.item_code}</td><td className={td}>{it.description}</td><td className={td}>{it.hsn_sac}</td>
                  <td className={td + " text-right"}>{num(it.quantity)}</td><td className={td}>{it.unit_of_measure}</td><td className={td + " text-right"}>{money(it.unit_price, i.currency)}</td>
                  <td className={td + " text-right"}>{money(it.taxable_value, i.currency)}</td><td className={td + " text-right"}>{it.tax_rate ?? "—"}</td>
                  <td className={td + " text-right"}>{money(it.tax_amount, i.currency)}</td><td className={td + " text-right"}>{money(it.line_total, i.currency)}</td><td className={td}><ConfidenceDot value={it.confidence} /></td>
                </tr>
              ))}
            </Table>
          </Card>
        </div>
      )}
      {tab === "audit" && (
        <Card title="Audit trail">
          <ul className="divide-y divide-zinc-100 text-sm dark:divide-zinc-800">
            {(ev.data ?? []).map((e, k) => (
              <li key={k} className="flex gap-3 py-2">
                <span className="w-32 shrink-0 text-xs text-zinc-500">{dateTime(e.created_at)}</span>
                <div><Badge tone={e.actor_type === "USER" ? "blue" : "gray"}>{e.event_type}</Badge> {e.event_description}
                  <span className="text-xs text-zinc-500"> · {e.actor_email ?? "system"}</span></div>
              </li>
            ))}
          </ul>
        </Card>
      )}
      <ReasonDialog open={dlg === "reject"} title="Reject invoice" danger confirm="Reject" onClose={() => setDlg(null)}
        onSubmit={async (r) => { await post(`/invoices/${id}/reject`, { reason: r }); reloadAll(); }} />
      <ReasonDialog open={dlg === "return"} title="Return to clerk" label="What should the clerk check or fix?" confirm="Return to clerk" onClose={() => setDlg(null)}
        extra={<p className="text-xs text-zinc-500">The invoice stays with the clerk until they resubmit it; the note is recorded in the audit trail.</p>}
        onSubmit={async (n) => { await post(`/invoices/${id}/return`, { note: n }); reloadAll(); }} />
      <ReasonDialog open={dlg === "submit"} title="Send for manager approval" label="Your summary for the manager" confirm="Send for approval" onClose={() => setDlg(null)}
        extra={<>
          <Field label="Recommendation">
            <select className={inputCls} value={recommend} onChange={(e) => setRecommend(e.target.value as "APPROVE" | "REJECT")}>
              <option value="APPROVE">Recommend approve</option>
              <option value="REJECT">Recommend reject</option>
            </select>
          </Field>
          {recommend === "APPROVE" && openExc.length > 0 && <p className="text-xs text-rose-700">{openExc.length} exception(s) are still open: mark each as checked (with a note) or resolve it first.</p>}
          {checkedExc.length > 0 && <p className="text-xs text-zinc-500">{checkedExc.length} checked exception(s) and their notes go to the manager with this.</p>}
        </>}
        onSubmit={async (n) => { await post(`/invoices/${id}/submit`, { recommendation: recommend, note: n }); reloadAll(); }} />
      <ReasonDialog open={dlg === "approve"} title="Approve against the system decision" label="Why is it OK to pay this invoice?" confirm="Approve" onClose={() => setDlg(null)}
        extra={<p className="text-xs text-zinc-500">The system decision is {i.decision}. Your approval and reason are recorded in the audit trail together with the open and checked exceptions.</p>}
        onSubmit={async (n) => { await post(`/invoices/${id}/approve`, { note: n }); reloadAll(); }} />
      <ReasonDialog open={dlg === "resubmit"} title="Resubmit for approval" label="What did you fix?" confirm="Resubmit" onClose={() => setDlg(null)}
        extra={i.return_note ? <p className="rounded bg-violet-50 px-2 py-1 text-xs text-violet-900">Manager&apos;s note: {i.return_note}</p> : undefined}
        onSubmit={async (n) => { await post(`/invoices/${id}/resubmit`, { note: n }); reloadAll(); }} />
      <ReasonDialog open={dlg === "override"} title="Override system decision" confirm="Apply override" onClose={() => setDlg(null)}
        extra={<>
          <Field label="Set status to">
            <select className={inputCls} value={target} onChange={(e) => setTarget(e.target.value)}>
              {["APPROVED", "REJECTED", "ON_HOLD", "REVIEW_REQUIRED"].map((s) => <option key={s}>{s}</option>)}
            </select>
          </Field>
          {openExc.length > 0 && <p className="text-xs text-amber-700">{openExc.length} open exception(s) will be recorded with this override.</p>}
        </>}
        onSubmit={async (r) => { await post(`/invoices/${id}/override`, { target_status: target, reason: r }); reloadAll(); }} />
    </>
  );
}

export default function InvoicePage() {
  return <Suspense fallback={<Loading />}><Detail /></Suspense>;
}
