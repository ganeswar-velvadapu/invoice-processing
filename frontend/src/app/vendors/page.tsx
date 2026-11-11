"use client";

import { useState } from "react";
import { post, put, type Page } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { useAuth } from "@/lib/auth";
import { Badge, Button, Card, Empty, ErrorBox, Field, inputCls, Loading, PageHeader, Table, td } from "@/components/ui";

interface Vendor { vendor_id: string; legal_name: string; gstin: string | null; address: string; is_approved: boolean; vendor_status: string; invoice_count: number; po_count: number;
  invoice_profile: Record<string, string> | null }

function AddVendor({ onDone }: { onDone: () => void }) {
  const [v, setV] = useState({ legal_name: "", gstin: "", address: "", is_approved: true, vendor_status: "ACTIVE" });
  const [err, setErr] = useState<string | null>(null);
  return (
    <Card title="Add or update vendor" className="mb-5">
      <form className="grid gap-3 md:grid-cols-5" onSubmit={async (e) => {
        e.preventDefault(); setErr(null);
        try { await post("/vendors", v); setV({ ...v, legal_name: "", gstin: "", address: "" }); onDone(); } catch (x) { setErr(x instanceof Error ? x.message : String(x)); }
      }}>
        <Field label="Legal name"><input required className={inputCls} value={v.legal_name} onChange={(e) => setV({ ...v, legal_name: e.target.value })} /></Field>
        <Field label="GSTIN"><input className={inputCls} value={v.gstin} onChange={(e) => setV({ ...v, gstin: e.target.value.toUpperCase() })} /></Field>
        <Field label="Address"><input className={inputCls} value={v.address} onChange={(e) => setV({ ...v, address: e.target.value })} /></Field>
        <Field label="Status">
          <select className={inputCls} value={v.vendor_status} onChange={(e) => setV({ ...v, vendor_status: e.target.value })}>{["ACTIVE", "INACTIVE", "BLOCKED"].map((s) => <option key={s}>{s}</option>)}</select>
        </Field>
        <div className="flex items-end gap-3">
          <label className="flex items-center gap-1.5 pb-2 text-sm"><input type="checkbox" checked={v.is_approved} onChange={(e) => setV({ ...v, is_approved: e.target.checked })} /> Approved</label>
          <Button variant="primary">Save</Button>
        </div>
      </form>
      <div className="mt-2"><ErrorBox error={err} /></div>
    </Card>
  );
}

function ProfileEditor({ v, onDone }: { v: Vendor; onDone: () => void }) {
  const [text, setText] = useState(JSON.stringify(v.invoice_profile ?? { invoice_number: "Bill\\s*No\\.?\\s*:?\\s*(\\S+)" }, null, 2));
  const [err, setErr] = useState<string | null>(null);
  return (
    <div className="mt-2 space-y-2">
      <p className="text-xs text-zinc-500">Vendor-specific extraction overrides: field name → regular expression with one capture group. Applied after generic extraction.</p>
      <textarea rows={5} className={inputCls + " font-mono text-xs"} value={text} onChange={(e) => setText(e.target.value)} />
      <ErrorBox error={err} />
      <Button onClick={async () => {
        setErr(null);
        try { await put(`/vendors/${v.vendor_id}/profile`, { document_type: "INVOICE", field_patterns: JSON.parse(text) }); onDone(); } catch (x) { setErr(x instanceof Error ? x.message : String(x)); }
      }}>Save profile</Button>
    </div>
  );
}

export default function Vendors() {
  const { can } = useAuth();
  const { data, error, reload } = useApi<Page<Vendor>>("/vendors?limit=500");
  const [edit, setEdit] = useState<string | null>(null);
  return (
    <>
      <PageHeader title="Vendors" subtitle="Invoices are only matched to approved, active vendors identified by GSTIN." />
      {can("AP_MANAGER") && <AddVendor onDone={reload} />}
      <ErrorBox error={error} />
      {!data ? <Loading /> : data.items.length === 0 ? <Empty>No vendors yet.</Empty> : (
        <Card>
          <Table head={["Vendor", "GSTIN", "Approved", "Status", "POs", "Invoices", "Extraction profile"]}>
            {data.items.map((v) => (
              <tr key={v.vendor_id}>
                <td className={td}><div className="font-medium">{v.legal_name}</div><div className="text-xs text-zinc-500">{v.address}</div></td>
                <td className={td + " font-mono text-xs"}>{v.gstin ?? "—"}</td>
                <td className={td}>{v.is_approved ? <Badge tone="green">Yes</Badge> : <Badge tone="red">No</Badge>}</td>
                <td className={td}><Badge value={v.vendor_status} tone={v.vendor_status === "ACTIVE" ? "green" : v.vendor_status === "BLOCKED" ? "red" : "gray"} /></td>
                <td className={td + " text-center"}>{v.po_count}</td><td className={td + " text-center"}>{v.invoice_count}</td>
                <td className={td + " max-w-sm"}>
                  {v.invoice_profile ? <span className="text-xs">{Object.keys(v.invoice_profile).join(", ")}</span> : <span className="text-xs text-zinc-400">generic</span>}
                  {can("ADMIN") && <button className="ml-2 text-xs text-sky-700 hover:underline" onClick={() => setEdit(edit === v.vendor_id ? null : v.vendor_id)}>{edit === v.vendor_id ? "close" : "edit"}</button>}
                  {edit === v.vendor_id && <ProfileEditor v={v} onDone={() => { setEdit(null); reload(); }} />}
                </td>
              </tr>
            ))}
          </Table>
        </Card>
      )}
    </>
  );
}
