#!/usr/bin/env bash
# One-command start for a new machine. Everything runs in Docker; the only prerequisite is
# Docker (Docker Desktop on macOS/Windows, Docker Engine + Compose plugin on Linux).
#
#   bash start.sh
#
# First run: asks for the database URL and an admin login, writes .env, downloads the local
# vision model (~11 GB including the Ollama image, once), builds and starts everything.
# Later runs: starts everything again, keeping all data. For a clean slate use scripts/reset_app.sh.
set -euo pipefail
cd "$(dirname "$0")"

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

# --- .env ----------------------------------------------------------------------------------------
setenv() { # setenv KEY VALUE: replace KEY=... in .env or append it (values may contain any characters)
  local tmp; tmp=$(mktemp)
  K="$1" V="$2" awk 'BEGIN{done=0} index($0, ENVIRON["K"] "=")==1 {print ENVIRON["K"] "=" ENVIRON["V"]; done=1; next} {print}
    END{if(!done) print ENVIRON["K"] "=" ENVIRON["V"]}' .env > "$tmp" && mv "$tmp" .env
}
getenv() { grep -E "^$1=" .env | tail -1 | cut -d= -f2- || true; }
ask() { # ask VAR "prompt" [default] [secret]
  local __v
  if [ "${4:-}" = secret ]; then read -r -s -p "$2: " __v; echo; else read -r -p "$2${3:+ [$3]}: " __v; fi
  printf -v "$1" '%s' "${__v:-${3:-}}"
}

if [ ! -f .env ]; then
  say "First run: creating .env"
  cp .env.example .env
  setenv DATABASE_URL ""
  setenv EXTRACTION_ENGINE hybrid
fi
chmod 600 .env

need_input=false
db="$(getenv DATABASE_URL)"; case "$db" in postgres*://*) case "$db" in *USER:PASSWORD*) need_input=true ;; esac ;; *) need_input=true ;; esac
[ -z "$(getenv ADMIN_PASSWORD)" ] && need_input=true
if $need_input && [ ! -t 0 ]; then fail ".env is incomplete and there is no terminal to ask: fill in DATABASE_URL and ADMIN_PASSWORD in .env"; fi

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
    echo "Skipped. You can add IMAP_* settings to .env later (see .env.example)."
  fi
  touch .start-asked-email
fi

# --- start everything (keeps existing data) ---------------------------------------------------
echo
say "Starting the app (frontend, API, worker, vision model)"
bash scripts/reset_app.sh --keep-data

echo
say "Ready: http://localhost:3000"
echo "Sign in as $(getenv ADMIN_EMAIL). For demo data see data/demo/README.md."
