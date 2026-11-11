"use client";

import Link from "next/link";
import { useState } from "react";

export function cx(...c: (string | false | null | undefined)[]) {
  return c.filter(Boolean).join(" ");
}

const tones: Record<string, string> = {
  green: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300",
  red: "bg-rose-50 text-rose-700 ring-rose-600/20 dark:bg-rose-500/10 dark:text-rose-300",
  amber: "bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300",
  blue: "bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300",
  violet: "bg-violet-50 text-violet-700 ring-violet-600/20 dark:bg-violet-500/10 dark:text-violet-300",
  gray: "bg-zinc-100 text-zinc-700 ring-zinc-500/20 dark:bg-zinc-500/10 dark:text-zinc-300",
};

const toneFor: Record<string, string> = {
  PASS: "green", APPROVE: "green", APPROVED: "green", READY_FOR_VERIFICATION: "green", PROCESSED: "green", INGESTED: "green", PARTIAL: "amber", NO_ATTACHMENTS: "gray", MATCHED: "green",
  CONFIRMED: "green", RESOLVED: "green", PAID: "green", DONE: "green",
  FAIL: "red", HOLD: "red", ON_HOLD: "red", REJECT: "red", REJECTED: "red", FAILED: "red", CRITICAL: "red", HIGH: "red",
  WARNING: "amber", REVIEW: "amber", REVIEW_REQUIRED: "amber", EXTRACTION_REVIEW: "amber", MEDIUM: "amber", CANDIDATE: "amber", OPEN: "amber",
  PENDING_DOCUMENTS: "blue", PENDING_PO_MATCH: "blue", PENDING_GRN: "blue", UNMATCHED: "blue", ASSIGNED: "blue",
  WAIVED: "violet", PAYMENT_PENDING: "violet", RETURNED: "violet", CHECKED: "green", AWAITING_APPROVAL: "blue",
};

export function Badge({ value, tone, children }: { value?: string | null; tone?: string; children?: React.ReactNode }) {
  const t = tone ?? toneFor[value ?? ""] ?? "gray";
  return (
    <span className={cx("inline-flex items-center whitespace-nowrap rounded-md px-1.5 py-0.5 text-xs font-medium ring-1 ring-inset", tones[t])}>
      {children ?? (value ? value.replace(/_/g, " ") : "—")}
    </span>
  );
}

export function ConfidenceDot({ value }: { value: number | null | undefined }) {
  if (value === null || value === undefined) return <span className="text-zinc-400">—</span>;
  const c = value >= 0.9 ? "bg-emerald-500" : value >= 0.75 ? "bg-amber-400" : "bg-rose-500";
  return (
    <span className="inline-flex items-center gap-1 text-xs tabular-nums text-zinc-500" title="Extraction confidence">
      <span className={cx("h-2 w-2 rounded-full", c)} />
      {Math.round(value * 100)}%
    </span>
  );
}

export function Card({ title, actions, children, className }: { title?: React.ReactNode; actions?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <section className={cx("rounded-lg border border-zinc-200 bg-white dark:border-zinc-800 dark:bg-zinc-900", className)}>
      {(title || actions) && (
        <header className="flex items-center justify-between gap-2 border-b border-zinc-200 px-4 py-2.5 dark:border-zinc-800">
          <h2 className="text-sm font-semibold">{title}</h2>
          <div className="flex items-center gap-2">{actions}</div>
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

export function Button({ variant = "secondary", className, ...p }: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "danger" | "ghost" }) {
  const v = {
    primary: "bg-blue-900 text-white shadow-sm hover:bg-blue-800",
    secondary: "border border-zinc-300 bg-white hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-900 dark:hover:bg-zinc-800",
    danger: "bg-rose-600 text-white hover:bg-rose-500",
    ghost: "hover:bg-zinc-100 dark:hover:bg-zinc-800",
  }[variant];
  return <button {...p} className={cx("inline-flex items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50", v, className)} />;
}

export const inputCls = "w-full rounded-md border border-zinc-300 bg-white px-2.5 py-1.5 text-sm outline-none focus:border-zinc-500 focus:ring-2 focus:ring-zinc-200 dark:border-zinc-700 dark:bg-zinc-950 dark:focus:ring-zinc-800";

export function Field({ label, children, hint }: { label: string; children: React.ReactNode; hint?: string }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-zinc-600 dark:text-zinc-400">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-zinc-500">{hint}</span>}
    </label>
  );
}

export function ErrorBox({ error }: { error: string | null | undefined }) {
  if (!error) return null;
  return <div className="rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-800 dark:border-rose-900 dark:bg-rose-950 dark:text-rose-200">{error}</div>;
}

export function Loading({ what = "Loading" }: { what?: string }) {
  return <div className="py-10 text-center text-sm text-zinc-500">{what}…</div>;
}

export function Empty({ children }: { children: React.ReactNode }) {
  return <div className="py-10 text-center text-sm text-zinc-500">{children}</div>;
}

export function PageHeader({ title, subtitle, actions }: { title: React.ReactNode; subtitle?: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold tracking-tight text-zinc-950">{title}</h1>
        {subtitle && <div className="mt-1 text-sm text-zinc-500">{subtitle}</div>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Table({ head, children }: { head: React.ReactNode[]; children: React.ReactNode }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-zinc-200 text-xs uppercase tracking-wide text-zinc-500 dark:border-zinc-800">
          <tr>{head.map((h, i) => <th key={i} className="whitespace-nowrap px-3 py-2 font-medium">{h}</th>)}</tr>
        </thead>
        <tbody className="divide-y divide-zinc-100 dark:divide-zinc-800">{children}</tbody>
      </table>
    </div>
  );
}

export const td = "px-3 py-2 align-top";

export function RowLink({ href, children }: { href: string; children: React.ReactNode }) {
  return <Link href={href} className="font-medium text-sky-700 hover:underline dark:text-sky-400">{children}</Link>;
}

/** Modal that collects a reason/note before running an action. */
export function ReasonDialog({ open, title, label = "Reason", confirm = "Confirm", danger, extra, onClose, onSubmit, minLength = 5 }: {
  open: boolean; title: string; label?: string; confirm?: string; danger?: boolean; extra?: React.ReactNode; minLength?: number;
  onClose: () => void; onSubmit: (text: string) => Promise<void>;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <div className="w-full max-w-md rounded-lg bg-white p-5 shadow-xl dark:bg-zinc-900" onClick={(e) => e.stopPropagation()}>
        <h3 className="mb-3 font-semibold">{title}</h3>
        <div className="space-y-3">
          {extra}
          <Field label={label}>
            <textarea autoFocus rows={3} className={inputCls} value={text} onChange={(e) => setText(e.target.value)} />
          </Field>
          <ErrorBox error={err} />
        </div>
        <div className="mt-4 flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button variant={danger ? "danger" : "primary"} disabled={busy || text.trim().length < minLength}
            onClick={async () => {
              setBusy(true);
              setErr(null);
              try {
                await onSubmit(text.trim());
                setText("");
                onClose();
              } catch (e) {
                setErr(e instanceof Error ? e.message : String(e));
              } finally {
                setBusy(false);
              }
            }}>
            {busy ? "Working…" : confirm}
          </Button>
        </div>
      </div>
    </div>
  );
}

export function Tabs({ tabs, value, onChange }: { tabs: { key: string; label: React.ReactNode }[]; value: string; onChange: (k: string) => void }) {
  return (
    <div className="mb-4 flex gap-1 overflow-x-auto border-b border-zinc-200 dark:border-zinc-800">
      {tabs.map((t) => (
        <button key={t.key} onClick={() => onChange(t.key)}
          className={cx("whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium", value === t.key ? "border-zinc-900 text-zinc-900 dark:border-zinc-100 dark:text-zinc-100" : "border-transparent text-zinc-500 hover:text-zinc-800 dark:hover:text-zinc-200")}>
          {t.label}
        </button>
      ))}
    </div>
  );
}
