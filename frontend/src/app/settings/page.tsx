"use client";

import { useState } from "react";
import { patch, post, type Page } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { useAuth } from "@/lib/auth";
import { Badge, Button, Card, ErrorBox, Field, inputCls, Loading, PageHeader, Table, td } from "@/components/ui";
import { dateTime } from "@/lib/format";

interface Tol { tolerance_rule_id: string; version: number; price_tolerance_pct: number; price_tolerance_abs: number; quantity_tolerance_pct: number; tax_rounding_abs: number;
  date_tolerance_days: number; min_extraction_confidence: number; is_active: boolean; note: string; created_at: string; created_by: string | null }
interface U { user_id: string; email: string; full_name: string; role: string; is_active: boolean }

function Tolerances() {
  const { can } = useAuth();
  const { data, reload } = useApi<Tol[]>("/tolerance-rules");
  const active = data?.find((t) => t.is_active);
  const [f, setF] = useState<Record<string, string> | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const form = f ?? (active ? { price_tolerance_pct: String(active.price_tolerance_pct), price_tolerance_abs: String(active.price_tolerance_abs), quantity_tolerance_pct: String(active.quantity_tolerance_pct),
    tax_rounding_abs: String(active.tax_rounding_abs), date_tolerance_days: String(active.date_tolerance_days), min_extraction_confidence: String(active.min_extraction_confidence), note: "" } : null);
  return (
    <Card title="Tolerance rules" className="mb-5">
      {!data ? <Loading /> : (
        <>
          <Table head={["Version", "Price", "Quantity", "Tax rounding", "Date", "Min. extraction conf.", "Note", "Created"]}>
            {data.map((t) => (
              <tr key={t.tolerance_rule_id}>
                <td className={td}>v{t.version} {t.is_active && <Badge tone="green">active</Badge>}</td>
                <td className={td}>±{t.price_tolerance_pct}% or ₹{t.price_tolerance_abs}</td><td className={td}>{t.quantity_tolerance_pct}%</td><td className={td}>₹{t.tax_rounding_abs}</td>
                <td className={td}>{t.date_tolerance_days} d</td><td className={td}>{t.min_extraction_confidence}</td><td className={td + " text-xs"}>{t.note}</td>
                <td className={td + " text-xs"}>{dateTime(t.created_at)}{t.created_by ? ` · ${t.created_by}` : ""}</td>
              </tr>
            ))}
          </Table>
          {can("ADMIN") && form && (
            <form className="mt-4 grid gap-3 md:grid-cols-4" onSubmit={async (e) => {
              e.preventDefault(); setErr(null);
              try {
                await post("/tolerance-rules", Object.fromEntries(Object.entries(form).map(([k, v]) => [k, k === "note" ? v : Number(v)])));
                setF(null); reload();
              } catch (x) { setErr(x instanceof Error ? x.message : String(x)); }
            }}>
              {([["price_tolerance_pct", "Price tolerance %"], ["price_tolerance_abs", "Price tolerance ₹"], ["quantity_tolerance_pct", "Quantity tolerance %"], ["tax_rounding_abs", "Tax rounding ₹"],
                ["date_tolerance_days", "Date tolerance (days)"], ["min_extraction_confidence", "Min. extraction confidence (0–1)"], ["note", "Change note"]] as const).map(([k, l]) => (
                <Field key={k} label={l}><input className={inputCls} value={form[k]} onChange={(e) => setF({ ...form, [k]: e.target.value })} /></Field>
              ))}
              <div className="flex items-end"><Button variant="primary">Publish new version</Button></div>
              <div className="md:col-span-4"><ErrorBox error={err} /></div>
            </form>
          )}
        </>
      )}
    </Card>
  );
}

function Users() {
  const { data, reload } = useApi<Page<U>>("/users?limit=200");
  const [n, setN] = useState({ email: "", full_name: "", password: "", role: "AP_CLERK" });
  const [err, setErr] = useState<string | null>(null);
  const run = async (fn: () => Promise<unknown>) => { setErr(null); try { await fn(); reload(); } catch (x) { setErr(x instanceof Error ? x.message : String(x)); } };
  return (
    <Card title="Users & roles">
      <ErrorBox error={err} />
      {!data ? <Loading /> : (
        <Table head={["Email", "Name", "Role", "Active", ""]}>
          {data.items.map((u) => (
            <tr key={u.user_id}>
              <td className={td}>{u.email}</td><td className={td}>{u.full_name}</td>
              <td className={td}>
                <select className={inputCls + " w-36"} value={u.role} onChange={(e) => run(() => patch(`/users/${u.user_id}`, { role: e.target.value }))}>
                  {["AP_CLERK", "AP_MANAGER", "ADMIN"].map((r) => <option key={r}>{r}</option>)}
                </select>
              </td>
              <td className={td}>{u.is_active ? <Badge tone="green">active</Badge> : <Badge tone="gray">disabled</Badge>}</td>
              <td className={td}><Button variant="ghost" onClick={() => run(() => patch(`/users/${u.user_id}`, { is_active: !u.is_active }))}>{u.is_active ? "Disable" : "Enable"}</Button></td>
            </tr>
          ))}
        </Table>
      )}
      <form className="mt-4 grid gap-3 md:grid-cols-5" onSubmit={(e) => { e.preventDefault(); run(async () => { await post("/users", { email: n.email, full_name: n.full_name, password: n.password, role: n.role }); setN({ email: "", full_name: "", password: "", role: "AP_CLERK" }); }); }}>
        <Field label="Email"><input required type="email" className={inputCls} value={n.email} onChange={(e) => setN({ ...n, email: e.target.value })} /></Field>
        <Field label="Full name"><input className={inputCls} value={n.full_name} onChange={(e) => setN({ ...n, full_name: e.target.value })} /></Field>
        <Field label="Initial password"><input required minLength={8} type="password" className={inputCls} value={n.password} onChange={(e) => setN({ ...n, password: e.target.value })} /></Field>
        <Field label="Role"><select className={inputCls} value={n.role} onChange={(e) => setN({ ...n, role: e.target.value })}>{["AP_CLERK", "AP_MANAGER", "ADMIN"].map((r) => <option key={r}>{r}</option>)}</select></Field>
        <div className="flex items-end"><Button variant="primary">Add user</Button></div>
      </form>
    </Card>
  );
}

export default function Settings() {
  const { can, user } = useAuth();
  return (
    <>
      <PageHeader title="Settings" subtitle={`Signed in as ${user?.email} (${user?.role})`} />
      <Tolerances />
      {can("ADMIN") && <Users />}
    </>
  );
}
