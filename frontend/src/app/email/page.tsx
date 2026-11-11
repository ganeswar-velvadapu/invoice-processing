"use client";

import { type Page } from "@/lib/api";
import { useApi } from "@/lib/hooks";
import { Badge, Card, Empty, ErrorBox, Loading, PageHeader, RowLink, Table, td } from "@/components/ui";
import { dateTime, docTypeLabel } from "@/lib/format";

interface EmailStatus { enabled: boolean; mailbox?: string; received: number; last_received_at?: string }
interface EmailDoc { document_id: string; original_filename: string; document_type: string; processing_status: string; invoice_id: string | null }
interface Email {
  email_message_id: string; from_address: string | null; from_name: string | null; subject: string | null; sent_at: string | null; received_at: string;
  attachments_found: number; documents_created: number; status: string; note: string | null; documents: EmailDoc[];
}

export default function EmailInbox() {
  const st = useApi<EmailStatus>("/email/status", { pollMs: 10000, pollWhile: () => true });
  const { data, error } = useApi<Page<Email>>("/email/messages?limit=100", { pollMs: 10000, pollWhile: () => true });
  const s = st.data;
  return (
    <>
      <PageHeader title="Email inbox" subtitle="Invoices emailed to the AP mailbox are picked up automatically and processed like uploads." />
      <ErrorBox error={error ?? st.error} />
      {s && (
        <div className={`mb-5 rounded-md border px-4 py-3 text-sm ${s.enabled ? "border-emerald-200 bg-emerald-50 text-emerald-900" : "border-zinc-200 bg-zinc-50 text-zinc-700"}`}>
          {s.enabled ? <>Watching <b>{s.mailbox}</b> · {s.received} email(s) received{s.last_received_at ? ` · last ${dateTime(s.last_received_at)}` : ""}. New mail is checked about every minute.</>
            : <>Email ingestion is off. Set IMAP_HOST, IMAP_USER and IMAP_PASSWORD in <code>.env</code> and restart the worker.</>}
        </div>
      )}
      {!data ? <Loading /> : data.items.length === 0 ? <Empty>No emails received yet. Send an invoice PDF to the mailbox.</Empty> : (
        <Card>
          <Table head={["Received", "From", "Subject", "Documents", "Status"]}>
            {data.items.map((m) => (
              <tr key={m.email_message_id}>
                <td className={td + " whitespace-nowrap text-xs"}>{dateTime(m.received_at)}</td>
                <td className={td + " text-sm"}>{m.from_name && <div className="font-medium">{m.from_name}</div>}<div className="text-xs text-zinc-500">{m.from_address}</div></td>
                <td className={td + " max-w-64 text-sm"}>{m.subject || <span className="text-zinc-400">(no subject)</span>}</td>
                <td className={td + " text-sm"}>
                  {m.documents.length === 0 && <span className="text-zinc-400">none</span>}
                  {m.documents.map((d) => (
                    <div key={d.document_id} className="flex flex-wrap items-center gap-1.5">
                      <RowLink href={`/documents/${d.document_id}`}>{d.original_filename}</RowLink>
                      <Badge tone="gray">{docTypeLabel(d.document_type)}</Badge><Badge value={d.processing_status} />
                      {d.invoice_id && <RowLink href={`/invoices/${d.invoice_id}`}>Invoice →</RowLink>}
                    </div>
                  ))}
                  {m.note && <div className="mt-1 text-xs text-amber-700">Skipped: {m.note}</div>}
                </td>
                <td className={td}><Badge value={m.status} /></td>
              </tr>
            ))}
          </Table>
        </Card>
      )}
    </>
  );
}
