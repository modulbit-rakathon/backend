#!/usr/bin/env bash
# Smoke test: one vision call with a JSON schema against the model gateway.
# Usage: AI_BASE_URL=... AI_API_KEY=... AI_MODEL_A=... scripts/smoke.sh path/to/photo.jpg [model]
set -euo pipefail

img="${1:?usage: smoke.sh <image> [model]}"
model="${2:-${AI_MODEL_A:?AI_MODEL_A not set}}"
: "${AI_BASE_URL:?AI_BASE_URL not set}"
: "${AI_API_KEY:?AI_API_KEY not set}"

case "$img" in
  *.png|*.PNG) mime=image/png ;;
  *) mime=image/jpeg ;;
esac

body=$(mktemp)
trap 'rm -f "$body"' EXIT

# The payload goes through a temp file: a base64 photo is too long for argv.
{
  printf '{"model":"%s","max_completion_tokens":4000,' "$model"
  printf '"messages":[{"role":"user","content":['
  printf '{"type":"text","text":"Přepiš názvy léků na fotce přesně, jak jsou napsané. Ignoruj osobní údaje."},'
  printf '{"type":"image_url","image_url":{"url":"data:%s;base64,' "$mime"
  base64 -w0 "$img"
  printf '"}}]}],'
  cat <<'EOF'
"response_format":{"type":"json_schema","json_schema":{"name":"smoke","strict":true,"schema":{
  "type":"object","additionalProperties":false,"required":["medications"],
  "properties":{"medications":{"type":"array","items":{
    "type":"object","additionalProperties":false,"required":["name","strength"],
    "properties":{"name":{"type":"string"},"strength":{"type":["string","null"]}}}}}}}}}
EOF
} >"$body"

curl -sS -w '\nHTTP %{http_code} in %{time_total}s\n' \
  -H "api-key: $AI_API_KEY" \
  -H 'Content-Type: application/json' \
  --data-binary "@$body" \
  "$AI_BASE_URL/chat/completions"
