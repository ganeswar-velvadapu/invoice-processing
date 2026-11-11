export function money(v: number | string | null | undefined, currency = "INR") {
  if (v === null || v === undefined || v === "") return "—";
  const n = typeof v === "string" ? Number(v) : v;
  if (Number.isNaN(n)) return String(v);
  return new Intl.NumberFormat("en-IN", { style: "currency", currency, minimumFractionDigits: 2 }).format(n);
}

export function num(v: number | string | null | undefined, digits = 3) {
  if (v === null || v === undefined || v === "") return "—";
  const n = typeof v === "string" ? Number(v) : v;
  return new Intl.NumberFormat("en-IN", { maximumFractionDigits: digits }).format(n);
}

export function date(v: string | null | undefined) {
  if (!v) return "—";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v;
  return d.toLocaleDateString("en-IN", { day: "2-digit", month: "short", year: "numeric" });
}

export function dateTime(v: string | null | undefined) {
  if (!v) return "—";
  return new Date(v).toLocaleString("en-IN", { day: "2-digit", month: "short", hour: "2-digit", minute: "2-digit" });
}

export function pct(v: number | string | null | undefined) {
  if (v === null || v === undefined) return "—";
  return `${Math.round(Number(v) * 100)}%`;
}

export const label = (s: string | null | undefined) => (s ? s.replace(/_/g, " ").toLowerCase().replace(/^\w/, (c) => c.toUpperCase()) : "—");

export const docTypes: { value: string; label: string }[] = [
  { value: "INVOICE", label: "Tax invoice" },
  { value: "PO", label: "Purchase order" },
  { value: "GRN", label: "Goods receipt note" },
  { value: "EWAY_BILL", label: "E-way bill" },
  { value: "MRN", label: "Material return note" },
  { value: "OTHER", label: "Other" },
];

export const docTypeLabel = (t: string | null | undefined) => docTypes.find((d) => d.value === t)?.label ?? label(t);
