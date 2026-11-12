#!/usr/bin/env bash
# Start the whole app from scratch: frontend, API, worker and the local vision model (Ollama + Qwen3-VL).
#
#   api       Go API on :8080 (logins, uploads, imports, approvals); runs the database migrations
#   worker    background processing: reads documents (rules or vision model), runs the checks and
#             matching, re-checks waiting invoices, and polls the email inbox when IMAP_* is set
#   frontend  Next.js UI on :3000
#   ollama    local vision model for scans and handwriting; the model is downloaded once (~3 GB)
#
# Default (fresh start) DELETES every uploaded document (storage volume) and every app table in the
# database from DATABASE_URL (users, vendors, POs, GRNs, invoices, emails, audit trail).
# Always KEEPS .env, data/, and the downloaded model (ollama-models volume).
# Ollama and the model are downloaded when missing, and restarted when already present.
#
# Usage (from the project root):
#   bash scripts/reset_app.sh               fresh start, asks for confirmation
#   bash scripts/reset_app.sh --yes         fresh start, no prompt
#   bash scripts/reset_app.sh --keep-data   just (re)build and start everything, nothing deleted
set -euo pipefail
cd "$(dirname "$0")/.."

KEEP_DATA=false
YES=false
for arg in "$@"; do
  case "$arg" in
    --keep-data) KEEP_DATA=true ;;
    --yes) YES=true ;;
    *) echo "unknown option: $arg"; exit 1 ;;
  esac
done

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
envval() { grep -E "^$1=" .env | tail -1 | cut -d= -f2- | tr -d '"'"'" || true; }

step "Checking prerequisites"
command -v docker >/dev/null || { echo "docker not found"; exit 1; }
docker compose version >/dev/null || { echo "docker compose not available"; exit 1; }
[ -f .env ] || { echo ".env missing: cp .env.example .env and fill in DATABASE_URL, JWT_SECRET, ADMIN_*"; exit 1; }
grep -qE '^DATABASE_URL=.+' .env || { echo "DATABASE_URL is not set in .env"; exit 1; }
MODEL="$(envval VLM_MODEL)"; MODEL="${MODEL:-qwen3-vl:4b-instruct}"
ENGINE="$(envval EXTRACTION_ENGINE)"; ENGINE="${ENGINE:-rules}"
echo "extraction engine: $ENGINE · vision model: $MODEL"
[ "$ENGINE" = "rules" ] && echo "note: EXTRACTION_ENGINE=rules, so scans and handwriting won't be read (use hybrid)"

if ! $KEEP_DATA; then
  if ! $YES; then
    echo
    echo "This permanently deletes ALL uploaded documents and ALL app data in the database from DATABASE_URL."
    echo "(Run with --keep-data to start without deleting anything.)"
    read -r -p "Type 'reset' to continue: " ans
    [ "$ans" = "reset" ] || { echo "Cancelled."; exit 1; }
  fi

  step "Stopping the app containers"
  docker compose rm -sf api worker frontend

  step "Deleting uploaded documents"
  docker volume rm invoice-processing_storage 2>/dev/null || echo "   (no storage volume)"

  step "Dropping the app tables in the database"
  docker run --rm --env-file .env postgres:17-alpine sh -c 'psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 -c "
  DROP TABLE IF EXISTS invoice_processing_events, invoice_exceptions, invoice_checks, tolerance_rules,
    invoice_grn_matches, invoice_po_item_matches, invoice_po_matches, invoice_items, invoices,
    grn_po_matches, goods_receipt_items, goods_receipt_notes, purchase_order_items, purchase_orders,
    processing_jobs, document_extractions, documents, email_messages, vendor_extraction_profiles, vendors, users,
    schema_migrations CASCADE;"'
fi

step "Ollama (vision model server)"
OLLAMA_IMAGE="ollama/ollama:$(envval OLLAMA_VERSION)"; [ "$OLLAMA_IMAGE" = "ollama/ollama:" ] && OLLAMA_IMAGE="ollama/ollama:0.40.11"
if docker image inspect "$OLLAMA_IMAGE" >/dev/null 2>&1; then
  echo "image $OLLAMA_IMAGE present"
else
  echo "image $OLLAMA_IMAGE not present: downloading (~8 GB, once)"
  docker compose --profile vlm pull ollama
fi
if [ -n "$(docker compose --profile vlm ps -q --status running ollama 2>/dev/null)" ]; then
  echo "restarting Ollama"
  docker compose --profile vlm restart ollama
else
  echo "starting Ollama"
  docker compose --profile vlm up -d ollama
fi
printf 'waiting for Ollama'
for _ in $(seq 1 90); do
  curl -sf http://127.0.0.1:11434/api/version >/dev/null && break
  printf '.'; sleep 2
done
echo
curl -sf http://127.0.0.1:11434/api/version >/dev/null || { echo "Ollama did not start: docker compose logs ollama"; exit 1; }

step "Checking the model $MODEL"
if docker compose --profile vlm exec -T ollama ollama list | awk 'NR>1 {print $1}' | grep -qx "$MODEL"; then
  echo "model present"
else
  avail_gb=$(df -Pk . | awk 'NR==2 {print int($4/1048576)}')
  echo "model not present: downloading (~3 GB, once); free disk: ${avail_gb} GB"
  [ "$avail_gb" -ge 4 ] || { echo "not enough free disk space"; exit 1; }
  docker compose --profile vlm exec -T ollama ollama pull "$MODEL"
fi

step "Building and starting api, worker and frontend"
docker compose --profile vlm up -d --build

step "Waiting for the API (runs database migrations on start)"
for _ in $(seq 1 90); do
  curl -sf http://localhost:8080/healthz >/dev/null && break
  sleep 2
done
curl -sf http://localhost:8080/healthz >/dev/null && echo "API is up" || { echo "API not up: docker compose logs api"; exit 1; }

step "Waiting for the frontend"
for _ in $(seq 1 60); do
  curl -s -o /dev/null http://localhost:3000/login && break
  sleep 2
done
curl -s -o /dev/null http://localhost:3000/login && echo "frontend is up" || echo "frontend not up yet: docker compose logs frontend"

step "Status"
docker compose --profile vlm ps --format '{{.Service}}: {{.Status}}'
sleep 3
if docker compose logs worker 2>/dev/null | grep -q "email ingestion started"; then
  echo "email ingestion: on ($(envval IMAP_USER))"
else
  echo "email ingestion: off (set IMAP_HOST, IMAP_USER, IMAP_PASSWORD in .env to enable)"
fi

echo
echo "Done. Open http://localhost:3000 and sign in with ADMIN_EMAIL / ADMIN_PASSWORD from .env."
if ! $KEEP_DATA; then
  echo "Then: create the clerk and manager users (Settings) and import data/demo/vendors.csv,"
  echo "purchase_orders.csv and goods_receipt_notes.csv (Import data). See data/demo/README.md."
fi
echo "Stop everything later (data kept):  docker compose --profile vlm stop"
