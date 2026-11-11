"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { useAuth } from "@/lib/auth";
import { cx } from "./ui";
import type { Role } from "@/lib/api";

type IconName = "dashboard" | "queue" | "alert" | "upload" | "mail" | "cart" | "truck" | "building" | "import" | "settings";

const nav: { href: string; label: string; icon: IconName; min?: Role }[] = [
  { href: "/", label: "Dashboard", icon: "dashboard" },
  { href: "/invoices", label: "Invoice queue", icon: "queue" },
  { href: "/exceptions", label: "Exceptions", icon: "alert" },
  { href: "/documents", label: "Documents & upload", icon: "upload" },
  { href: "/email", label: "Email inbox", icon: "mail" },
  { href: "/purchase-orders", label: "Purchase orders", icon: "cart" },
  { href: "/grns", label: "Goods receipts", icon: "truck" },
  { href: "/vendors", label: "Vendors", icon: "building" },
  { href: "/imports", label: "Import data", icon: "import", min: "AP_MANAGER" },
  { href: "/settings", label: "Settings", icon: "settings" },
];

const paths: Record<IconName | "logout" | "menu" | "logo", string> = {
  dashboard: "M3 13h8V3H3v10zm0 8h8v-6H3v6zm10 0h8V11h-8v10zm0-18v6h8V3h-8z",
  queue: "M4 6h16M4 12h16M4 18h10",
  alert: "M12 9v4m0 4h.01M10.3 3.9L1.8 18a2 2 0 001.7 3h17a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z",
  upload: "M4 16v2a2 2 0 002 2h12a2 2 0 002-2v-2M12 4v12m0-12l-4 4m4-4l4 4",
  mail: "M4 6h16a1 1 0 011 1v10a1 1 0 01-1 1H4a1 1 0 01-1-1V7a1 1 0 011-1zm0 1l8 6 8-6",
  cart: "M3 3h2l2.4 12.2a2 2 0 002 1.8h8.4a2 2 0 002-1.6L21 8H6M9 21h.01M18 21h.01",
  truck: "M3 7h11v9H3zM14 10h4l3 3v3h-7M7.5 19a1.5 1.5 0 100-3 1.5 1.5 0 000 3zM17.5 19a1.5 1.5 0 100-3 1.5 1.5 0 000 3z",
  building: "M4 21V5a2 2 0 012-2h8a2 2 0 012 2v16M16 9h2a2 2 0 012 2v10M8 7h4M8 11h4M8 15h4M3 21h18",
  import: "M12 3v12m0 0l-4-4m4 4l4-4M4 17v2a2 2 0 002 2h12a2 2 0 002-2v-2",
  settings: "M12 15a3 3 0 100-6 3 3 0 000 6zM19.4 15a1.7 1.7 0 00.3 1.8l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.7 1.7 0 00-1.8-.3 1.7 1.7 0 00-1 1.5V21a2 2 0 11-4 0v-.1a1.7 1.7 0 00-1.1-1.5 1.7 1.7 0 00-1.8.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.7 1.7 0 00.3-1.8 1.7 1.7 0 00-1.5-1H3a2 2 0 110-4h.1a1.7 1.7 0 001.5-1.1 1.7 1.7 0 00-.3-1.8l-.1-.1a2 2 0 112.8-2.8l.1.1a1.7 1.7 0 001.8.3H9a1.7 1.7 0 001-1.5V3a2 2 0 114 0v.1a1.7 1.7 0 001 1.5 1.7 1.7 0 001.8-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.7 1.7 0 00-.3 1.8V9a1.7 1.7 0 001.5 1H21a2 2 0 110 4h-.1a1.7 1.7 0 00-1.5 1z",
  logout: "M15 3h4a2 2 0 012 2v14a2 2 0 01-2 2h-4M10 17l5-5-5-5M15 12H3",
  menu: "M4 6h16M4 12h16M4 18h16",
  logo: "M9 12l2 2 4-4M7 3h7l5 5v11a2 2 0 01-2 2H7a2 2 0 01-2-2V5a2 2 0 012-2z",
};

function Icon({ name, className }: { name: keyof typeof paths; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round"
      className={cx("h-[18px] w-[18px] shrink-0", className)} aria-hidden="true">
      <path d={paths[name]} />
    </svg>
  );
}

const roleLabel: Record<Role, string> = { AP_CLERK: "AP Clerk", AP_MANAGER: "AP Manager", ADMIN: "Administrator" };

function initials(name: string, email: string) {
  const src = name.trim() || email.split("@")[0];
  const parts = src.split(/[\s._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase() || "U";
}

export default function Shell({ children }: { children: React.ReactNode }) {
  const { user, ready, logout, can } = useAuth();
  const path = usePathname();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const isLogin = path.startsWith("/login");

  useEffect(() => {
    if (ready && !user && !isLogin) router.replace("/login");
  }, [ready, user, isLogin, router]);

  if (isLogin) return <>{children}</>;
  if (!ready || !user) return <div className="p-10 text-center text-sm text-zinc-500">Loading…</div>;

  return (
    <div className="min-h-screen bg-white md:flex">
      {/* Sidebar: slide-in drawer on mobile, fixed full-height column on desktop */}
      <aside
        className={cx(
          "fixed inset-y-0 left-0 z-40 flex w-64 flex-col border-r border-zinc-200 bg-zinc-50 transition-transform duration-200",
          "md:sticky md:top-0 md:h-screen md:shrink-0 md:translate-x-0",
          open ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <div className="flex items-center gap-2.5 border-b border-zinc-200 px-5 py-4">
          <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-900 text-white">
            <Icon name="logo" className="h-[18px] w-[18px]" />
          </span>
          <div className="leading-tight">
            <div className="text-[15px] font-semibold text-zinc-900">AP Invoice Matching</div>
            <div className="text-xs text-zinc-500">Invoice · PO · GRN</div>
          </div>
        </div>

        <nav className="flex-1 space-y-0.5 overflow-y-auto px-3 py-4">
          {nav.filter((n) => !n.min || can(n.min)).map((n) => {
            const active = n.href === "/" ? path === "/" : path.startsWith(n.href);
            return (
              <Link key={n.href} href={n.href} onClick={() => setOpen(false)}
                className={cx(
                  "flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors",
                  active ? "bg-white font-semibold text-blue-900 shadow-sm ring-1 ring-zinc-200" : "text-zinc-700 hover:bg-zinc-100 hover:text-zinc-900",
                )}>
                <Icon name={n.icon} className={active ? "text-blue-900" : "text-zinc-500"} />
                {n.label}
              </Link>
            );
          })}
        </nav>

        <div className="border-t border-zinc-200 p-3">
          <div className="flex items-center gap-3 rounded-lg px-2 py-2">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-blue-100 text-sm font-semibold text-blue-900">
              {initials(user.full_name, user.email)}
            </span>
            <div className="min-w-0">
              <div className="truncate text-sm font-medium text-zinc-900">{user.full_name || user.email.split("@")[0]}</div>
              <div className="truncate text-xs text-zinc-500" title={user.email}>{user.email}</div>
              <span className="mt-1 inline-block rounded bg-zinc-200/70 px-1.5 py-0.5 text-[11px] font-medium text-zinc-700">{roleLabel[user.role]}</span>
            </div>
          </div>
          <button onClick={logout}
            className="mt-2 flex w-full items-center justify-center gap-2 rounded-md border border-zinc-300 bg-white px-3 py-2 text-sm font-medium text-zinc-700 shadow-sm transition-colors hover:border-red-300 hover:bg-red-50 hover:text-red-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500">
            <Icon name="logout" />
            Sign out
          </button>
        </div>
      </aside>
      {open && <div className="fixed inset-0 z-30 bg-black/30 md:hidden" onClick={() => setOpen(false)} />}

      <div className="min-w-0 flex-1">
        <div className="sticky top-0 z-20 flex items-center gap-3 border-b border-zinc-200 bg-white px-4 py-2.5 md:hidden">
          <button onClick={() => setOpen(true)} aria-label="Open menu" className="rounded-md border border-zinc-300 p-1.5 text-zinc-700">
            <Icon name="menu" />
          </button>
          <span className="text-sm font-semibold">AP Invoice Matching</span>
        </div>
        <main className="mx-auto max-w-7xl px-4 py-6 md:px-8 md:py-8">{children}</main>
      </div>
    </div>
  );
}
