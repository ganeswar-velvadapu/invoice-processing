#!/usr/bin/env bash
# Install and smoke-test the local vision model (Ollama + Qwen3-VL) for document extraction.
# Everything runs on this machine; no document leaves it.
#
# Usage (from the project root):
#   bash scripts/setup_vlm.sh [PDF_TO_TEST]
#
# Settings (env vars, optional):
#   VLM_MODEL=qwen3-vl:4b-instruct   model to pull (faster alternative: qwen3-vl:2b-instruct)
#   OLLAMA_VERSION=0.40.11           ollama/ollama image tag
#   OLLAMA_CPUS=6                    CPU cores the model may use (rest stay free for you)
#
# Remove everything later:
#   docker compose rm -sf ollama
#   docker volume rm invoice-processing_ollama-models
#   docker rmi ollama/ollama:${OLLAMA_VERSION:-0.40.11}
set -euo pipefail

cd "$(dirname "$0")/.."
MODEL="${VLM_MODEL:-qwen3-vl:4b-instruct}"
TEST_PDF="${1:-data/demo/invoices/09_ganga_handwritten.pdf}"

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

step "Checking prerequisites"
command -v docker >/dev/null || { echo "docker not found"; exit 1; }
command -v pdftoppm >/dev/null || { echo "pdftoppm not found (install poppler-utils)"; exit 1; }
command -v python3 >/dev/null || { echo "python3 not found"; exit 1; }
avail_gb=$(df -BG --output=avail . | tail -1 | tr -dc '0-9')
echo "free disk: ${avail_gb} GB (need ~8 GB)"
[ "$avail_gb" -ge 8 ] || { echo "not enough free disk space"; exit 1; }

step "Downloading the Ollama image (ollama/ollama:${OLLAMA_VERSION:-0.40.11}, ~3.4 GB, once)"
docker compose --profile vlm pull ollama

step "Starting Ollama (limited to ${OLLAMA_CPUS:-6} CPU cores)"
docker compose --profile vlm up -d ollama
printf 'waiting for Ollama'
for _ in $(seq 1 60); do
  if curl -sf http://127.0.0.1:11434/api/version >/dev/null; then break; fi
  printf '.'; sleep 2
done
echo
curl -sf http://127.0.0.1:11434/api/version || { echo "Ollama did not start; see: docker compose logs ollama"; exit 1; }
echo

step "Downloading the model ${MODEL} (~3 GB, once)"
docker compose exec ollama ollama pull "$MODEL"
docker compose exec ollama ollama list

if [ -f "$TEST_PDF" ]; then
  step "Smoke test on $(basename "$TEST_PDF") (first page; on CPU this can take a few minutes)"
  python3 scripts/vlm_smoke.py "$TEST_PDF" --model "$MODEL"
else
  echo "Test PDF not found ($TEST_PDF); run later: python3 scripts/vlm_smoke.py FILE.pdf --model $MODEL"
fi

step "Done"
echo "Stop when not needed:   docker compose stop ollama"
echo "Start again:            docker compose --profile vlm up -d ollama"
