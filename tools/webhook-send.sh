#!/usr/bin/env bash
# Send a test event to the daemon's capture intake and report what landed.
#
# The intake is POST /webhooks/capture/{source} (see
# docs/prds/event-capture-storage.md): it bypasses the dashboard token, redacts
# the headers and body it stores, and the Event inspector reads it back. A
# payload whose shape a field mapping knows is what makes the rest of the
# Events chain (mapping -> binding -> dispatched task) testable by hand.
#
# Usage:
#   tools/webhook-send.sh                      # one generic JSON event
#   tools/webhook-send.sh --kind github-issue  # realistic GitHub issues delivery
#   tools/webhook-send.sh --kind sentry --source sentry-prod
#   tools/webhook-send.sh --body '{"a":1}'     # an exact payload
#   tools/webhook-send.sh --file /tmp/event.json
#   tools/webhook-send.sh --count 5 --delay 0.5
#   tools/webhook-send.sh --list               # what each kind sends, and exit
#
# Environment:
#   ARCHIE_URL   dashboard base URL (default http://127.0.0.1:8484)
#
# Limits the intake enforces (defaults from [capture] in config.example.toml):
#   rate 1/s per remote address, burst 5  -> a fast --count > 5 needs --delay
#   bodies over 256 KiB are rejected with 413
# Both are per-daemon-configurable, so a 429/413 here is the daemon's policy,
# not a broken sender.
set -euo pipefail

BASE_URL="${ARCHIE_URL:-http://127.0.0.1:8484}"
SOURCE="manual-test"
KIND="generic"
COUNT=1
DELAY=0
BODY=""
FILE=""
QUIET=0

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
}

list_kinds() {
  cat <<'KINDS'
Kinds (what each one posts):
  generic        {"hello":"world","n":1}                       small JSON
  github-issue   GitHub "issues" delivery (action/issue/repository/sender)
  github-push    GitHub "push" delivery (ref/repository/commits/pusher)
  sentry         Sentry "issue created" delivery
  form           application/x-www-form-urlencoded body
  large          ~64 KB JSON, to exercise the intake's body bound
  empty          no body at all
KINDS
}

while [ $# -gt 0 ]; do
  case "$1" in
    --source) SOURCE="${2:?--source needs a value}"; shift 2 ;;
    --kind)   KIND="${2:?--kind needs a value}"; shift 2 ;;
    --count)  COUNT="${2:?--count needs a value}"; shift 2 ;;
    --delay)  DELAY="${2:?--delay needs a value}"; shift 2 ;;
    --body)   BODY="${2:?--body needs a value}"; shift 2 ;;
    --file)   FILE="${2:?--file needs a value}"; shift 2 ;;
    --url)    BASE_URL="${2:?--url needs a value}"; shift 2 ;;
    --quiet)  QUIET=1; shift ;;
    --list)   list_kinds; exit 0 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; echo >&2; usage >&2; exit 2 ;;
  esac
done

# A body from --body or --file wins over --kind: the caller is testing an exact
# payload, not a shape.
CONTENT_TYPE="application/json"
if [ -n "$BODY" ]; then
  PAYLOAD="$BODY"
elif [ -n "$FILE" ]; then
  PAYLOAD="$(cat "$FILE")"
else
  case "$KIND" in
    generic) PAYLOAD='{"hello":"world","n":1}' ;;
    github-issue)
      PAYLOAD='{"action":"opened","issue":{"number":42,"title":"Test issue from webhook-send.sh","body":"Filed by tools/webhook-send.sh.","html_url":"https://github.com/samcharles93/archie-core/issues/42"},"repository":{"full_name":"samcharles93/archie-core","html_url":"https://github.com/samcharles93/archie-core"},"sender":{"login":"webhook-send"}}'
      ;;
    github-push)
      PAYLOAD='{"ref":"refs/heads/main","repository":{"full_name":"samcharles93/archie-core","html_url":"https://github.com/samcharles93/archie-core"},"commits":[{"id":"c0ffee1","message":"Test commit from webhook-send.sh"}],"pusher":{"name":"webhook-send"}}'
      ;;
    sentry)
      PAYLOAD='{"action":"created","data":{"issue":{"title":"Test error from webhook-send.sh","culprit":"app/main.go"}},"project":"archie"}'
      ;;
    form)
      CONTENT_TYPE="application/x-www-form-urlencoded"
      PAYLOAD='event=test&source=webhook-send.sh&detail=nothing-to-see'
      ;;
    large)
      # ~64 KB of filler inside a valid JSON string.
      PAYLOAD="$(printf '{"filler":"'; head -c 65536 /dev/zero | tr '\0' 'x'; printf '"}')"
      ;;
    empty) PAYLOAD="" ;;
    *) echo "unknown kind: $KIND" >&2; echo >&2; list_kinds >&2; exit 2 ;;
  esac
fi

send_one() {
  local url="${BASE_URL%/}/webhooks/capture/${SOURCE}"
  local code
  if [ -n "$PAYLOAD" ]; then
    code="$(curl -sS -o /tmp/webhook-send.out -w '%{http_code}' \
      -X POST "$url" -H "Content-Type: ${CONTENT_TYPE}" --data-binary "$PAYLOAD")" || {
      echo "cannot reach ${BASE_URL} — is the daemon running? (task dev, or archied)" >&2
      return 1
    }
  else
    code="$(curl -sS -o /tmp/webhook-send.out -w '%{http_code}' \
      -X POST "$url" -H "Content-Type: ${CONTENT_TYPE}")" || {
      echo "cannot reach ${BASE_URL} — is the daemon running? (task dev, or archied)" >&2
      return 1
    }
  fi
  if [ "$code" -eq 429 ]; then
    echo "POST ${url} -> 429: the intake's rate limit (default 1/s, burst 5 per address)." >&2
    echo "Space sends out with --delay 1, or raise [capture] rate_per_second / rate_burst." >&2
    return 1
  fi
  if [ "$code" -eq 413 ]; then
    echo "POST ${url} -> 413: body over [capture] max_body_bytes (default 256 KiB)." >&2
    return 1
  fi
  if [ "$code" -lt 200 ] || [ "$code" -ge 300 ]; then
    echo "POST ${url} -> ${code}: $(cat /tmp/webhook-send.out)" >&2
    return 1
  fi
  [ "$QUIET" -eq 1 ] || echo "POST ${url} -> ${code} (source: ${SOURCE}, kind: ${KIND})"
}

i=0
while [ "$i" -lt "$COUNT" ]; do
  send_one
  i=$((i + 1))
  [ "$i" -lt "$COUNT" ] && [ "$DELAY" != "0" ] && sleep "$DELAY"
done

# Read it back, so a silent drop is visible here rather than only in the UI.
if [ "$QUIET" -eq 0 ]; then
  sleep 0.3
  echo
  echo "Latest captures:"
  curl -sS "${BASE_URL%/}/api/captures?limit=${COUNT}" \
    | tr ',' '\n' | grep -E '"id"|"source"|"received_at"|"content_type"' | sed 's/^ *//' | head -20
  echo
  echo "Open ${BASE_URL%/}/captures to inspect them."
fi
