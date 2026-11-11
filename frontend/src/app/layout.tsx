import type { Metadata } from "next";
import { Inter } from "next/font/google";
import { Suspense } from "react";
import "./globals.css";
import { AuthProvider } from "@/lib/auth";
import Shell from "@/components/Shell";

const inter = Inter({ subsets: ["latin"], display: "swap", variable: "--font-inter" });

export const metadata: Metadata = {
  title: "AP Invoice Matching",
  description: "Invoice processing and PO/GRN reconciliation",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={`${inter.variable} h-full antialiased`}>
      <body className="min-h-full bg-white text-zinc-950">
        <AuthProvider>
          <Suspense fallback={<div className="p-10 text-center text-sm text-zinc-500">Loading…</div>}>
            <Shell>{children}</Shell>
          </Suspense>
        </AuthProvider>
      </body>
    </html>
  );
}
