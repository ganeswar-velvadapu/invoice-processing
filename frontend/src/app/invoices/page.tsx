"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { useApi } from "@/lib/hooks";
import type { Page } from "@/lib/api";
import { Badge, Card, ConfidenceDot, Empty, ErrorBox, inputCls, Loading, PageHeader, RowLink, Table, Tabs, td } from "@/components/ui";
import { date, money } from "@/lib/format";

interface Inv {
  invoice_id: string; invoice_number: string | null; invoice_date: string | null; seller_name: string | null; vendor_name: string | null;
  grand_total: number | null; currency: string; invoice_status: string; decision: string | null; decision_summary: string | null;
  extraction_confidence: number | null; matched_pos: string[]; open_exceptions: number; po_references: string[];
}

const tabs = [
  { key: "", label: "All" },
  { key: "READY_FOR_VERIFICATION", label: "Ready for approval" },
  { key: "REVIEW_REQUIRED", label: "Review" },
  { key: "ON_HOLD", label: "On hold" },
  { key: "AWAITING_APPROVAL", label: "Awaiting approval" },
  { key: "RETURNED", label: "Returned to clerk" },
  { key: "PENDING_PO_MATCH,PENDING_GRN,PENDING_DOCUMENTS", label: "Waiting for documents" },
  { key: "APPROVED", label: "Approved" },
  { key: "REJECTED", label: "Rejected" },
];

function Queue() {
  const sp = useSearchParams();
  const router = useRouter();
  const status = sp.get("status") ?? "";
  const [q, setQ] = useState(sp.get("q") ?? "");
  const [offset, setOffset] = useState(0);
  const path = `/invoices?status=${encodeURIComponent(status)}&q=${encodeURIComponent(sp.get("q") ?? "")}&limit=50&offset=${offset}`;
  const { data, error } = useApi<Page<Inv>>(path);
  const setParam = (k: string, v: string) => {
    const p = new URLSearchParams(sp.toString());
    if (v) p.set(k, v); else p.delete(k);
    setOffset(0);
    router.replace(`/invoices?${p}`);
  };
  return (
    <>
      <PageHeader title="Invoice queue" subtitle="System decisions with their reasons; open an invoice to review, compare and act."
        actions={<form onSubmit={(e) => { e.preventDefault(); setParam("q", q); }}><input className={inputCls + " w-64"} placeholder="Search number, supplier, GSTIN" value={q} onChange={(e) => setQ(e.target.value)} /></form>} />
      <Tabs tabs={tabs} value={status} onChange={(k) => setParam("status", k)} />
      <ErrorBox error={error} />
      {!data ? <Loading /> : data.items.length === 0 ? <Empty>No invoices here.</Empty> : (
        <Card>
          <Table head={["Invoice", "Supplier", "Date", "Total", "PO", "Status", "Decision / reason", "Exc.", "Conf."]}>
            {data.items.map((i) => (
              <tr key={i.invoice_id} className="hover:bg-zinc-50 dark:hover:bg-zinc-800/40">
                <td className={td + " whitespace-nowrap"}><RowLink href={`/invoices/${i.invoice_id}`}>{i.invoice_number ?? "(no number)"}</RowLink></td>
                <td className={td + " max-w-48 truncate"}>{i.vendor_name ?? i.seller_name ?? "—"}</td>
                <td className={td + " whitespace-nowrap"}>{date(i.invoice_date)}</td>
                <td className={td + " whitespace-nowrap text-right"}>{money(i.grand_total, i.currency)}</td>
                <td className={td + " whitespace-nowrap text-xs"}>{i.matched_pos.length ? i.matched_pos.join(", ") : <span className="text-zinc-400">{i.po_references.join(", ") || "—"}</span>}</td>
                <td className={td}><Badge value={i.invoice_status} /></td>
                <td className={td + " max-w-md"}><div className="flex items-start gap-2"><Badge value={i.decision} /><span className="line-clamp-2 text-xs text-zinc-600 dark:text-zinc-400">{i.decision_summary}</span></div></td>
                <td className={td + " text-center"}>{i.open_exceptions || ""}</td>
                <td className={td}><ConfidenceDot value={i.extraction_confidence} /></td>
              </tr>
            ))}
          </Table>
          <div className="mt-3 flex items-center justify-between text-xs text-zinc-500">
            <span>{data.total} invoice(s)</span>
            <div className="flex gap-2">
              <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))} className="disabled:opacity-40">← Prev</button>
              <button disabled={offset + 50 >= data.total} onClick={() => setOffset(offset + 50)} className="disabled:opacity-40">Next →</button>
            </div>
          </div>
        </Card>
      )}
    </>
  );
}

export default function InvoicesPage() {
  return <Suspense fallback={<Loading />}><Queue /></Suspense>;
}
