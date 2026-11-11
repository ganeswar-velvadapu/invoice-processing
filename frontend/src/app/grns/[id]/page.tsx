"use client";

import { useParams } from "next/navigation";
import { Suspense } from "react";
import { useApi } from "@/lib/hooks";
import { Card, ErrorBox, Loading, PageHeader, RowLink, Table, td } from "@/components/ui";
import { date, num } from "@/lib/format";

interface GRN { goods_receipt_note_id: string; grn_number: string; receipt_date: string | null; receipt_status: string; po_reference: string | null; purchase_order_id: string | null;
  po_number: string | null; vendor_name: string | null; source: string; document_id: string | null;
  items: { goods_receipt_item_id: string; line_number: number; item_code: string; description: string; quantity_received: number; quantity_accepted: number; quantity_rejected: number; unit_of_measure: string; po_line_number: number | null }[] }

function Detail() {
  const { id } = useParams<{ id: string }>();
  const { data: g, error } = useApi<GRN>(`/goods-receipt-notes/${id}`);
  if (error) return <ErrorBox error={error} />;
  if (!g) return <Loading />;
  return (
    <>
      <PageHeader title={`GRN ${g.grn_number}`} subtitle={<>{g.vendor_name ?? "—"} · {date(g.receipt_date)} · {g.receipt_status} · PO {g.purchase_order_id ? <RowLink href={`/purchase-orders/${g.purchase_order_id}`}>{g.po_number}</RowLink> : `${g.po_reference ?? "—"} (not on file)`}
        {g.document_id && <> · <RowLink href={`/documents/${g.document_id}`}>original</RowLink></>}</>} />
      <Card title="Received lines">
        <Table head={["#", "Item", "Received", "Accepted", "Rejected", "PO line"]}>
          {g.items.map((i) => (
            <tr key={i.goods_receipt_item_id}>
              <td className={td}>{i.line_number}</td><td className={td}><div className="font-medium">{i.item_code}</div><div className="text-xs text-zinc-500">{i.description}</div></td>
              <td className={td + " text-right"}>{num(i.quantity_received)} {i.unit_of_measure}</td><td className={td + " text-right"}>{num(i.quantity_accepted)}</td>
              <td className={td + " text-right"}>{num(i.quantity_rejected)}</td><td className={td}>{i.po_line_number ?? <span className="text-amber-700">unlinked</span>}</td>
            </tr>
          ))}
        </Table>
      </Card>
    </>
  );
}

export default function GRNPage() {
  return <Suspense fallback={<Loading />}><Detail /></Suspense>;
}
