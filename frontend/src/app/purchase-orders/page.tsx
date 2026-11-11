"use client";

import { useState } from "react";
import type { Page } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { Badge, Card, Empty, ErrorBox, inputCls, Loading, PageHeader, RowLink, Table, td } from "@/components/ui";
import { date, money } from "@/lib/format";

interface PO { purchase_order_id: string; po_number: string; po_date: string | null; po_status: string; total_amount: number | null; currency: string; source: string;
  vendor_name: string | null; vendor_gstin: string | null; line_count: number; grn_count: number; invoice_count: number }

export default function POList() {
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const { data, error } = useApi<Page<PO>>(`/purchase-orders?q=${encodeURIComponent(search)}&limit=200`);
  return (
    <>
      <PageHeader title="Purchase orders" subtitle="Imported master data and POs extracted from uploaded documents."
        actions={<form onSubmit={(e) => { e.preventDefault(); setSearch(q); }}><input className={inputCls + " w-64"} placeholder="Search PO or vendor" value={q} onChange={(e) => setQ(e.target.value)} /></form>} />
      <ErrorBox error={error} />
      {!data ? <Loading /> : data.items.length === 0 ? <Empty>No purchase orders. Import them or upload PO documents.</Empty> : (
        <Card>
          <Table head={["PO", "Vendor", "Date", "Total", "Lines", "GRNs", "Invoices", "Status", "Source"]}>
            {data.items.map((p) => (
              <tr key={p.purchase_order_id}>
                <td className={td}><RowLink href={`/purchase-orders/${p.purchase_order_id}`}>{p.po_number}</RowLink></td>
                <td className={td}>{p.vendor_name ?? <span className="text-zinc-500">{p.vendor_gstin ?? "—"} (no vendor record)</span>}</td>
                <td className={td}>{date(p.po_date)}</td>
                <td className={td + " text-right"}>{money(p.total_amount, p.currency)}</td>
                <td className={td + " text-center"}>{p.line_count}</td><td className={td + " text-center"}>{p.grn_count}</td><td className={td + " text-center"}>{p.invoice_count}</td>
                <td className={td}><Badge value={p.po_status} tone={p.po_status === "APPROVED" ? "green" : "gray"} /></td>
                <td className={td + " text-xs"}>{p.source}</td>
              </tr>
            ))}
          </Table>
        </Card>
      )}
    </>
  );
}
