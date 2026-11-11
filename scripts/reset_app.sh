#!/usr/bin/env bash
# Reset the app to an empty state and start it again.
#
#   DELETES: every uploaded document (storage volume) and every app table in the database
#            (users, vendors, POs, GRNs, invoices, checks, exceptions, audit trail).
#   KEEPS:   .env, the downloaded vision model (ollama-models volume), data/*.csv.
#
# Usage (from the project root):  bash scripts/reset_app.sh        (asks for confirmation)
#                                  bash scripts/reset_app.sh --yes  (no prompt)
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "${1:-}" != "--yes" ]; then
  echo "This permanently deletes ALL uploaded documents and ALL data in the database from DATABASE_URL."
  read -r -p "Type 'reset' to continue: " ans
  [ "$ans" = "reset" ] || { echo "Cancelled."; exit 1; }
fi

echo "==> Stopping app containers (Ollama keeps running)"
docker compose rm -sf api worker frontend

echo "==> Deleting uploaded documents"
docker volume rm invoice-processing_storage 2>/dev/null || echo "   (no storage volume)"

echo "==> Dropping app tables in the database"
docker run --rm --env-file .env postgres:17-alpine sh -c 'psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 -c "
DROP TABLE IF EXISTS invoice_processing_events, invoice_exceptions, invoice_checks, tolerance_rules,
  invoice_grn_matches, invoice_po_item_matches, invoice_po_matches, invoice_items, invoices,
  grn_po_matches, goods_receipt_items, goods_receipt_notes, purchase_order_items, purchase_orders,
  processing_jobs, document_extractions, documents, email_messages, vendor_extraction_profiles, vendors, users,
  schema_migrations CASCADE;"'

echo "==> Starting Ollama (if not running) and rebuilding the app"
docker compose --profile vlm up -d ollama
docker compose up -d --build

echo "==> Waiting for the API"
for _ in $(seq 1 60); do
  if curl -sf http://localhost:8080/healthz >/dev/null; then break; fi
  sleep 2
done
curl -sf http://localhost:8080/healthz >/dev/null && echo "API is up" || { echo "API not up yet: docker compose logs api"; exit 1; }
docker compose --profile vlm ps --format '{{.Service}}: {{.Status}}'
echo
echo "Done. Open http://localhost:3000 and sign in with ADMIN_EMAIL / ADMIN_PASSWORD from .env."
echo "Then: create the clerk + manager users, and import data/vendors.csv, purchase_orders.csv, goods_receipt_notes.csv."
