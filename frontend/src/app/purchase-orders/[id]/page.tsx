"use client";

import { useParams } from "next/navigation";
import { Suspense } from "react";
import { useApi } from "@/lib/hooks";
import { Badge, Card, cx, ErrorBox, Loading, PageHeader, RowLink, Table, td } from "@/components/ui";
import { date, money, num } from "@/lib/format";

interface POItem { purchase_order_item_id: string; line_number: number; item_code: string; description: string; hsn_sac: string; quantity_ordered: number; unit_of_measure: string;
  unit_price: number; tax_rate: number; line_total: number; quantity_received: number; quantity_accepted: number; quantity_invoiced: number }
interface PO { purchase_order_id: string; po_number: string; po_date: string | null; po_status: string; currency: string; total_amount: number; source: string; document_id: string | null;
  vendor_name: string | null; vendor_gstin: string | null; buyer_entity: string; items: POItem[];
  goods_receipts: { goods_receipt_note_id: string; grn_number: string; receipt_date: string; receipt_status: string }[];
  invoices: { invoice_id: string; invoice_number: string; invoice_date: string; grand_total: number; invoice_status: string; decision: string; match_method: string; match_status: string }[] }

function Detail() {
  const { id } = useParams<{ id: string }>();
  const { data: p, error } = useApi<PO>(`/purchase-orders/${id}`);
  if (error) return <ErrorBox error={error} />;
  if (!p) return <Loading />;
  return (
    <>
      <PageHeader title={<span className="flex items-center gap-2">PO {p.po_number} <Badge value={p.po_status} tone={p.po_status === "APPROVED" ? "green" : "gray"} /></span>}
        subtitle={<>{p.vendor_name ?? p.vendor_gstin} · {date(p.po_date)} · {money(p.total_amount, p.currency)} · source {p.source}{p.document_id && <> · <RowLink href={`/documents/${p.document_id}`}>original</RowLink></>}</>} />
      <Card title="Lines: ordered, received, accepted and invoiced" className="mb-5">
        <Table head={["#", "Item", "Ordered", "Price", "GST", "Received", "Accepted", "Invoiced", "Open to invoice"]}>
          {p.items.map((i) => {
            const open = Math.min(i.quantity_ordered, i.quantity_accepted) - i.quantity_invoiced;
            return (
              <tr key={i.purchase_order_item_id}>
                <td className={td}>{i.line_number}</td>
                <td className={td}><div className="font-medium">{i.item_code}</div><div className="text-xs text-zinc-500">{i.description}</div></td>
                <td className={td + " text-right"}>{num(i.quantity_ordered)} {i.unit_of_measure}</td>
                <td className={td + " text-right"}>{money(i.unit_price, p.currency)}</td><td className={td + " text-right"}>{i.tax_rate}%</td>
                <td className={td + " text-right"}>{num(i.quantity_received)}</td><td className={td + " text-right"}>{num(i.quantity_accepted)}</td>
                <td className={cx(td, "text-right", i.quantity_invoiced > i.quantity_accepted && "font-semibold text-rose-600")}>{num(i.quantity_invoiced)}</td>
                <td className={cx(td, "text-right", open < 0 && "text-rose-600")}>{num(open)}</td>
              </tr>
            );
          })}
        </Table>
      </Card>
      <div className="grid gap-5 md:grid-cols-2">
        <Card title="Goods receipts">
          {p.goods_receipts.length === 0 ? <p className="text-sm text-zinc-500">None yet.</p> : <ul className="text-sm">{p.goods_receipts.map((g) => <li key={g.goods_receipt_note_id}><RowLink href={`/grns/${g.goods_receipt_note_id}`}>{g.grn_number}</RowLink> · {date(g.receipt_date)} · {g.receipt_status}</li>)}</ul>}
        </Card>
        <Card title="Invoices">
          {p.invoices.length === 0 ? <p className="text-sm text-zinc-500">None yet.</p> : <ul className="space-y-1 text-sm">{p.invoices.map((i) => <li key={i.invoice_id} className="flex flex-wrap items-center gap-2"><RowLink href={`/invoices/${i.invoice_id}`}>{i.invoice_number}</RowLink> {money(i.grand_total, p.currency)} <Badge value={i.invoice_status} /><span className="text-xs text-zinc-500">{i.match_method.replace(/_/g, " ").toLowerCase()} · {i.match_status.toLowerCase()}</span></li>)}</ul>}
        </Card>
      </div>
    </>
  );
}

export default function POPage() {
  return <Suspense fallback={<Loading />}><Detail /></Suspense>;
}
