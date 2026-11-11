"use client";

import type { Page } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { Card, Empty, ErrorBox, Loading, PageHeader, RowLink, Table, td } from "@/components/ui";
import { date } from "@/lib/format";

interface GRN { goods_receipt_note_id: string; grn_number: string; receipt_date: string | null; receipt_status: string; po_reference: string | null; purchase_order_id: string | null;
  po_number: string | null; vendor_name: string | null; line_count: number; unlinked_lines: number; source: string }

export default function GRNList() {
  const { data, error } = useApi<Page<GRN>>(`/goods-receipt-notes?limit=200`);
  return (
    <>
      <PageHeader title="Goods receipts" subtitle="Received and accepted quantities used for three-way matching." />
      <ErrorBox error={error} />
      {!data ? <Loading /> : data.items.length === 0 ? <Empty>No goods receipts yet.</Empty> : (
        <Card>
          <Table head={["GRN", "PO", "Vendor", "Date", "Lines", "Status", "Source"]}>
            {data.items.map((g) => (
              <tr key={g.goods_receipt_note_id}>
                <td className={td}><RowLink href={`/grns/${g.goods_receipt_note_id}`}>{g.grn_number}</RowLink></td>
                <td className={td}>{g.purchase_order_id ? <RowLink href={`/purchase-orders/${g.purchase_order_id}`}>{g.po_number}</RowLink> : <span className="text-amber-700">{g.po_reference ?? "—"} (PO not on file)</span>}</td>
                <td className={td}>{g.vendor_name ?? "—"}</td><td className={td}>{date(g.receipt_date)}</td>
                <td className={td}>{g.line_count}{g.unlinked_lines > 0 && <span className="text-xs text-amber-700"> ({g.unlinked_lines} unlinked)</span>}</td>
                <td className={td}>{g.receipt_status}</td><td className={td + " text-xs"}>{g.source}</td>
              </tr>
            ))}
          </Table>
        </Card>
      )}
    </>
  );
}
