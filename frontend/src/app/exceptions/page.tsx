"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { Suspense } from "react";
import type { Page } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { Badge, Card, Empty, ErrorBox, inputCls, Loading, PageHeader, RowLink, Table, td } from "@/components/ui";
import { dateTime, label, money } from "@/lib/format";

interface Exc { invoice_exception_id: string; invoice_id: string; invoice_number: string | null; seller_name: string | null; grand_total: number | null; invoice_status: string;
  exception_code: string; severity: string; description: string; exception_status: string; assigned_to: string | null; created_at: string }

function List() {
  const sp = useSearchParams();
  const router = useRouter();
  const status = sp.get("status") ?? "OPEN,ASSIGNED";
  const code = sp.get("code") ?? "";
  const severity = sp.get("severity") ?? "";
  const { data, error } = useApi<Page<Exc>>(`/exceptions?status=${status}&code=${code}&severity=${severity}&limit=200`);
  const set = (k: string, v: string) => { const p = new URLSearchParams(sp.toString()); if (v) p.set(k, v); else p.delete(k); router.replace(`/exceptions?${p}`); };
  return (
    <>
      <PageHeader title="Exceptions" subtitle="Issues raised by validation and matching. Resolve or waive them from the invoice." />
      <div className="mb-4 flex flex-wrap gap-2">
        <select className={inputCls + " w-44"} value={status} onChange={(e) => set("status", e.target.value)}>
          <option value="OPEN,ASSIGNED">Open</option><option value="RESOLVED">Resolved</option><option value="WAIVED">Waived</option><option value="OPEN,ASSIGNED,RESOLVED,WAIVED">All</option>
        </select>
        <select className={inputCls + " w-36"} value={severity} onChange={(e) => set("severity", e.target.value)}>
          <option value="">Any severity</option>{["CRITICAL", "HIGH", "MEDIUM", "LOW"].map((s) => <option key={s}>{s}</option>)}
        </select>
        {code && <button className="text-sm text-sky-700 hover:underline" onClick={() => set("code", "")}>Clear filter: {label(code)} ✕</button>}
      </div>
      <ErrorBox error={error} />
      {!data ? <Loading /> : data.items.length === 0 ? <Empty>No exceptions match.</Empty> : (
        <Card>
          <Table head={["Severity", "Exception", "Invoice", "Supplier", "Amount", "Invoice status", "Raised", "Status"]}>
            {data.items.map((e) => (
              <tr key={e.invoice_exception_id}>
                <td className={td}><Badge value={e.severity} /></td>
                <td className={td + " max-w-lg"}><div className="font-medium">{label(e.exception_code)}</div><div className="line-clamp-2 text-xs text-zinc-500">{e.description}</div></td>
                <td className={td}><RowLink href={`/invoices/${e.invoice_id}`}>{e.invoice_number ?? "(no number)"}</RowLink></td>
                <td className={td}>{e.seller_name}</td>
                <td className={td + " whitespace-nowrap text-right"}>{money(e.grand_total)}</td>
                <td className={td}><Badge value={e.invoice_status} /></td>
                <td className={td + " whitespace-nowrap text-xs"}>{dateTime(e.created_at)}</td>
                <td className={td}><Badge value={e.exception_status} />{e.assigned_to && <div className="text-xs">{e.assigned_to}</div>}</td>
              </tr>
            ))}
          </Table>
        </Card>
      )}
    </>
  );
}

export default function ExceptionsPage() {
  return <Suspense fallback={<Loading />}><List /></Suspense>;
}
