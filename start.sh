#!/usr/bin/env bash
# One-command start for a new machine. Everything runs in Docker; the only prerequisite is
# Docker (Docker Desktop on macOS/Windows, Docker Engine + Compose plugin on Linux).
#
#   bash start.sh
#
# First run: asks for the database URL and an admin login, writes backend/.env (and frontend/.env), downloads the local
# vision model (~11 GB including the Ollama image, once), builds and starts everything.
#
# Every run starts CLEAN: it first runs scripts/reset_app.sh, which removes the previous run's containers, images,
# uploaded documents and database data, so you get an empty app with just the first admin.
# Kept: backend/.env, frontend/.env, data/, Ollama and the model.
#
#   bash start.sh              clean start (deletes uploads and database data)
#   bash start.sh --keep-data  start without deleting anything
set -euo pipefail
cd "$(dirname "$0")"

RESET_MODE="--yes"   # clean start by default
case "${1:-}" in
  --keep-data) RESET_MODE="--keep-data" ;;
  "") ;;
  *) echo "unknown option: $1 (use --keep-data to keep existing data)"; exit 1 ;;
esac

say() { printf '\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\033[31m%s\033[0m\n' "$*"; exit 1; }

# --- prerequisites -----------------------------------------------------------------------------
command -v docker >/dev/null || fail "Docker is not installed. Install Docker Desktop (macOS/Windows) or Docker Engine (Linux): https://docs.docker.com/get-docker/"
docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is missing (the 'docker compose' command). Update Docker Desktop or install the compose plugin."
docker info >/dev/null 2>&1 || fail "Docker is installed but not running. Start Docker Desktop / the docker service and run this again."
command -v curl >/dev/null || fail "curl is required."

free_gb=$(df -Pk . | awk 'NR==2 {print int($4/1048576)}')
if [ "$free_gb" -lt 15 ]; then
  echo "Warning: only ${free_gb} GB free disk. The first start needs about 15 GB (vision model + images)."
fi

# --- .env files -----------------------------------------------------------------------------------
ENV_FILE=backend/.env   # backend settings (database, login, email, vision model)
[ -f frontend/.env ] || cp frontend/.env.example frontend/.env   # frontend: only where the backend is
setenv() { # setenv KEY VALUE: replace KEY=... in .env or append it (values may contain any characters)
  local tmp; tmp=$(mktemp)
  K="$1" V="$2" awk 'BEGIN{done=0} index($0, ENVIRON["K"] "=")==1 {print ENVIRON["K"] "=" ENVIRON["V"]; done=1; next} {print}
    END{if(!done) print ENVIRON["K"] "=" ENVIRON["V"]}' "$ENV_FILE" > "$tmp" && mv "$tmp" "$ENV_FILE"
}
getenv() { grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true; }
ask() { # ask VAR "prompt" [default] [secret]
  local __v
  if [ "${4:-}" = secret ]; then read -r -s -p "$2: " __v; echo; else read -r -p "$2${3:+ [$3]}: " __v; fi
  printf -v "$1" '%s' "${__v:-${3:-}}"
}

if [ ! -f "$ENV_FILE" ]; then
  say "First run: creating $ENV_FILE"
  cp backend/.env.example "$ENV_FILE"
  setenv DATABASE_URL ""
fi
chmod 600 "$ENV_FILE"

need_input=false
db="$(getenv DATABASE_URL)"; case "$db" in postgres*://*) case "$db" in *USER:PASSWORD*) need_input=true ;; esac ;; *) need_input=true ;; esac
[ -z "$(getenv ADMIN_PASSWORD)" ] && need_input=true
if $need_input && [ ! -t 0 ]; then fail "$ENV_FILE is incomplete and there is no terminal to ask: fill in DATABASE_URL and ADMIN_PASSWORD in $ENV_FILE"; fi

case "$db" in postgres*://*) case "$db" in *USER:PASSWORD*) db="" ;; esac ;; *) db="" ;; esac
if [ -z "$db" ]; then
  echo
  echo "The app needs a PostgreSQL database. A free Neon database works: https://neon.tech"
  echo "(create a project, then copy its connection string)"
  while :; do
    ask db "PostgreSQL connection string (postgresql://...)" "" secret
    case "$db" in postgres*://*) break ;; *) echo "That doesn't look like a postgresql:// URL, try again." ;; esac
  done
  setenv DATABASE_URL "$db"
fi

if [ -z "$(getenv JWT_SECRET)" ]; then
  setenv JWT_SECRET "$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"
  echo "Generated JWT_SECRET."
fi

if [ -z "$(getenv ADMIN_PASSWORD)" ]; then
  echo
  echo "The first admin account is created on first start."
  ask email "Admin email" "$(getenv ADMIN_EMAIL)"
  [ -z "$email" ] && email="admin@example.com"
  while :; do
    ask pw "Admin password (8+ characters)" "" secret
    [ "${#pw}" -ge 8 ] && break
    echo "Too short."
  done
  setenv ADMIN_EMAIL "$email"
  setenv ADMIN_PASSWORD "$pw"
fi

if [ -z "$(getenv IMAP_HOST)" ] && [ -t 0 ] && [ ! -f .start-asked-email ]; then
  echo
  ask yn "Set up email ingestion now (Gmail with an app password)? y/N" "n"
  if [ "$yn" = y ] || [ "$yn" = Y ]; then
    echo "Gmail: turn on 2-Step Verification, then create an app password at https://myaccount.google.com/apppasswords"
    ask user "Gmail address"
    ask pass "App password (16 characters, spaces are fine)" "" secret
    setenv IMAP_HOST imap.gmail.com
    setenv IMAP_PORT 993
    setenv IMAP_USER "$user"
    setenv IMAP_PASSWORD "$pass"
    setenv IMAP_POLL_INTERVAL 30s
  else
    echo "Skipped. You can add IMAP_* settings to backend/.env later (see backend/.env.example)."
  fi
  touch .start-asked-email
fi

# --- clean up (unless --keep-data) ----------------------------------------------------------------
echo
if [ "$RESET_MODE" = "--yes" ]; then
  say "Clean start: removing the previous run (containers, images, uploads, database data; Ollama and the model are kept)"
  bash scripts/reset_app.sh --yes
else
  say "Keeping existing data"
fi

# --- start everything ----------------------------------------------------------------------------
step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
MODEL="$(getenv VLM_MODEL | tr -d '"'"'")"; MODEL="${MODEL:-qwen3-vl:4b-instruct}"
OLLAMA_IMAGE="ollama/ollama:$(getenv OLLAMA_VERSION | tr -d '"'"'")"; [ "$OLLAMA_IMAGE" = "ollama/ollama:" ] && OLLAMA_IMAGE="ollama/ollama:0.40.11"

step "Ollama (vision model server)"
if docker image inspect "$OLLAMA_IMAGE" >/dev/null 2>&1; then
  echo "image $OLLAMA_IMAGE present"
else
  echo "image $OLLAMA_IMAGE not present: downloading (~8 GB, once)"
  docker compose --profile vlm pull ollama
fi
docker compose --profile vlm up -d ollama
printf 'waiting for Ollama'
for _ in $(seq 1 90); do
  curl -sf http://127.0.0.1:11434/api/version >/dev/null && break
  printf '.'; sleep 2
done
echo
curl -sf http://127.0.0.1:11434/api/version >/dev/null || fail "Ollama did not start: docker compose logs ollama"

step "Checking the model $MODEL"
if docker compose --profile vlm exec -T ollama ollama list | awk 'NR>1 {print $1}' | grep -qx "$MODEL"; then
  echo "model present"
else
  avail_gb=$(df -Pk . | awk 'NR==2 {print int($4/1048576)}')
  echo "model not present: downloading (~3 GB, once); free disk: ${avail_gb} GB"
  [ "$avail_gb" -ge 4 ] || fail "not enough free disk space"
  docker compose --profile vlm exec -T ollama ollama pull "$MODEL"
fi

step "Building and starting the backend (api) and frontend"
docker compose --profile vlm up -d --build --remove-orphans

step "Waiting for the API (creates the database tables on start)"
for _ in $(seq 1 90); do
  curl -sf http://localhost:8080/healthz >/dev/null && break
  sleep 2
done
curl -sf http://localhost:8080/healthz >/dev/null && echo "API is up" || fail "API not up: docker compose logs api"

step "Waiting for the frontend"
for _ in $(seq 1 60); do
  curl -s -o /dev/null http://localhost:3000/login && break
  sleep 2
done
curl -s -o /dev/null http://localhost:3000/login && echo "frontend is up" || echo "frontend not up yet: docker compose logs frontend"

step "Status"
docker compose --profile vlm ps --format '{{.Service}}: {{.Status}}'
sleep 3
if docker compose logs api 2>/dev/null | grep -q "email ingestion started"; then
  echo "email ingestion: on ($(getenv IMAP_USER))"
else
  echo "email ingestion: off (set IMAP_HOST, IMAP_USER, IMAP_PASSWORD in backend/.env to enable)"
fi

echo
say "Ready: http://localhost:3000"
echo "Sign in as $(getenv ADMIN_EMAIL) (password: ADMIN_PASSWORD in backend/.env)."
if [ "$RESET_MODE" = "--yes" ]; then
  echo "The app is empty: create the clerk and manager users (Settings) and import the demo data (see data/demo/README.md)."
fi
echo "Stop everything (data kept):  docker compose --profile vlm stop"
echo "Remove everything except the model:  bash scripts/reset_app.sh"
