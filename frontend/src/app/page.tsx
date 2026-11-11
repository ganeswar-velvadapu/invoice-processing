"use client";

import Link from "next/link";
import { useApi } from "@/lib/hooks";
import { Badge, Card, ErrorBox, Loading, PageHeader } from "@/components/ui";
import { dateTime, label, money } from "@/lib/format";

interface Summary {
  invoices_by_status: Record<string, number>;
  invoices_by_decision: Record<string, number>;
  documents_by_status: Record<string, number>;
  open_exceptions_by_severity: Record<string, number>;
  open_exceptions_by_code: Record<string, number>;
  awaiting_approval_amount: number;
  jobs: Record<string, number>;
  recent_events: { event_type: string; event_description: string; created_at: string; invoice_id: string | null; document_id: string | null; actor_email: string | null }[];
}

function Stat({ label, value, href, sub, tone }: { label: string; value: React.ReactNode; href: string; sub?: string; tone?: string }) {
  return (
    <Link href={href} className="block rounded-lg border border-zinc-200 bg-white p-4 hover:border-zinc-400 dark:border-zinc-800 dark:bg-zinc-900">
      <div className="text-xs font-medium text-zinc-500">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${tone ?? ""}`}>{value}</div>
      {sub && <div className="mt-0.5 text-xs text-zinc-500">{sub}</div>}
    </Link>
  );
}

export default function Dashboard() {
  const { data, error } = useApi<Summary>("/dashboard/summary", { pollMs: 5000, pollWhile: () => true });
  if (error) return <ErrorBox error={error} />;
  if (!data) return <Loading />;
  const s = data.invoices_by_status;
  const openExc = Object.values(data.open_exceptions_by_severity).reduce((a, b) => a + b, 0);
  const processing = (data.jobs.QUEUED ?? 0) + (data.jobs.RUNNING ?? 0);
  return (
    <>
      <PageHeader title="Dashboard" subtitle={processing ? `${processing} document job(s) processing…` : "All documents processed"} />
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="Ready for approval" value={s.READY_FOR_VERIFICATION ?? 0} sub={money(data.awaiting_approval_amount)} href="/invoices?status=READY_FOR_VERIFICATION" tone="text-emerald-600" />
        <Stat label="Review required" value={s.REVIEW_REQUIRED ?? 0} href="/invoices?status=REVIEW_REQUIRED" tone="text-amber-600" />
        <Stat label="On hold" value={s.ON_HOLD ?? 0} href="/invoices?status=ON_HOLD" tone="text-rose-600" />
        <Stat label="Awaiting approval" value={s.AWAITING_APPROVAL ?? 0} href="/invoices?status=AWAITING_APPROVAL" tone="text-blue-800" />
        <Stat label="Returned to clerk" value={s.RETURNED ?? 0} href="/invoices?status=RETURNED" tone="text-violet-700" />
        <Stat label="Waiting for PO / GRN" value={(s.PENDING_PO_MATCH ?? 0) + (s.PENDING_GRN ?? 0) + (s.PENDING_DOCUMENTS ?? 0)} href="/invoices?status=PENDING_PO_MATCH,PENDING_GRN,PENDING_DOCUMENTS" tone="text-sky-600" />
        <Stat label="Open exceptions" value={openExc} sub={data.open_exceptions_by_severity.HIGH ? `${data.open_exceptions_by_severity.HIGH} high` : undefined} href="/exceptions" />
        <Stat label="Extraction review" value={data.documents_by_status.EXTRACTION_REVIEW ?? 0} sub={data.documents_by_status.FAILED ? `${data.documents_by_status.FAILED} failed` : undefined} href="/documents?status=EXTRACTION_REVIEW" />
      </div>
      <div className="mt-6 grid gap-6 lg:grid-cols-3">
        <Card title="Open exceptions by type">
          {Object.keys(data.open_exceptions_by_code).length === 0 ? <p className="text-sm text-zinc-500">No open exceptions.</p> : (
            <ul className="space-y-1.5 text-sm">
              {Object.entries(data.open_exceptions_by_code).sort((a, b) => b[1] - a[1]).map(([k, v]) => (
                <li key={k} className="flex justify-between"><Link className="hover:underline" href={`/exceptions?code=${k}`}>{label(k)}</Link><span className="tabular-nums text-zinc-500">{v}</span></li>
              ))}
            </ul>
          )}
        </Card>
        <Card title="Recent activity" className="lg:col-span-2">
          <ul className="divide-y divide-zinc-100 text-sm dark:divide-zinc-800">
            {data.recent_events.map((e, i) => (
              <li key={i} className="flex gap-3 py-2">
                <span className="w-28 shrink-0 text-xs text-zinc-500">{dateTime(e.created_at)}</span>
                <div className="min-w-0">
                  <Badge tone="gray">{e.event_type}</Badge>{" "}
                  {e.invoice_id ? <Link href={`/invoices/${e.invoice_id}`} className="hover:underline">{e.event_description}</Link>
                    : e.document_id ? <Link href={`/documents/${e.document_id}`} className="hover:underline">{e.event_description}</Link> : e.event_description}
                  {e.actor_email && <span className="text-xs text-zinc-500"> · {e.actor_email}</span>}
                </div>
              </li>
            ))}
          </ul>
        </Card>
      </div>
    </>
  );
}
