#!/usr/bin/env bash
# Remove everything this project put on your machine, except the vision model. Starts nothing.
#
# DELETES:
#   - the containers (api, frontend, ollama) and the project's Docker network
#   - the images built for this project (invoice-processing-*)
#   - uploaded documents (the invoice-processing_storage volume)
#   - every app table in the database from DATABASE_URL (users, vendors, POs, GRNs, invoices, emails, audit trail)
# STOPS anything still listening on the app's ports, 8080 (backend) and 3000 (frontend), e.g. `go run` / `npm run dev`
# KEEPS:
#   - the Ollama image and the downloaded model (invoice-processing_ollama-models volume)
#   - backend/.env, frontend/.env and data/
#   - images you tagged for Docker Hub (yourname/...), which you can remove yourself with docker rmi
#   - the postgres:17-alpine image (used here to empty the database)
#
# Usage (from the project root):
#   bash scripts/reset_app.sh         asks for confirmation
#   bash scripts/reset_app.sh --yes   no prompt (start.sh uses this)
set -euo pipefail
cd "$(dirname "$0")/.."

YES=false
case "${1:-}" in
  --yes) YES=true ;;
  "") ;;
  *) echo "unknown option: $1"; exit 1 ;;
esac

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ENV_FILE=backend/.env

command -v docker >/dev/null || { echo "docker not found"; exit 1; }

if ! $YES; then
  echo "This permanently deletes this project's containers, images, uploaded documents"
  echo "and ALL app data in the database from DATABASE_URL. Ollama and the model are kept."
  read -r -p "Type 'reset' to continue: " ans
  [ "$ans" = "reset" ] || { echo "Cancelled."; exit 1; }
fi

step "Dropping the app tables in the database"
if [ -f "$ENV_FILE" ] && grep -qE '^DATABASE_URL=.+' "$ENV_FILE"; then
  docker run --rm --env-file "$ENV_FILE" postgres:17-alpine sh -c 'psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 -c "
  DROP TABLE IF EXISTS invoice_processing_events, invoice_exceptions, invoice_checks, tolerance_rules,
    invoice_grn_matches, invoice_po_item_matches, invoice_po_matches, invoice_items, invoices,
    grn_po_matches, goods_receipt_items, goods_receipt_notes, purchase_order_items, purchase_orders,
    processing_jobs, document_extractions, documents, email_messages, vendor_extraction_profiles, vendors, users,
    schema_migrations CASCADE;"'
  echo "database emptied"
else
  echo "skipped: no DATABASE_URL in $ENV_FILE"
fi

step "Removing containers, the project network and the images built for this project"
# --rmi local removes images Compose built here; the downloaded ollama/ollama image is kept.
# No -v: volumes are kept here (the model volume must survive); uploads are removed below.
docker compose --profile vlm down --remove-orphans --rmi local
for img in $(docker images --format '{{.Repository}}:{{.Tag}}' | grep '^invoice-processing-' || true); do
  docker rmi "$img" >/dev/null && echo "removed image $img"
done

step "Deleting uploaded documents"
docker volume rm invoice-processing_storage >/dev/null 2>&1 && echo "removed volume invoice-processing_storage" \
  || echo "(no uploads volume)"

step "Stopping anything else on the app's ports (8080 backend, 3000 frontend)"
# Docker's containers are already gone; this catches a backend or frontend started outside Docker.
pids_on_port() {
  if command -v lsof >/dev/null; then lsof -ti "tcp:$1" -sTCP:LISTEN 2>/dev/null || true
  else ss -ltnpH "sport = :$1" 2>/dev/null | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u || true
  fi
}
for port in 8080 3000; do
  pids="$(pids_on_port "$port")"
  if [ -z "$pids" ]; then
    echo "port $port: free"
    continue
  fi
  for pid in $pids; do
    echo "port $port: stopping pid $pid ($(ps -o args= -p "$pid" 2>/dev/null | cut -c1-80))"
    kill "$pid" 2>/dev/null || true
  done
  sleep 2
  for pid in $pids; do
    kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true   # still running: force it
  done
  [ -z "$(pids_on_port "$port")" ] && echo "port $port: free" \
    || echo "port $port: still in use (owned by another user? try: sudo lsof -i :$port)"
done

step "Left on this machine"
docker images --format '  image:  {{.Repository}}:{{.Tag}}  {{.Size}}' | grep -i ollama || true
docker volume ls --format '  volume: {{.Name}}' | grep -i ollama || true

echo
echo "Reset done. Start again with: bash start.sh"
echo "Optional: free Docker's build cache (shared with your other projects): docker builder prune"
