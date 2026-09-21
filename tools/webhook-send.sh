#!/usr/bin/env bash
# Send a test event to the daemon's inbound webhook intakes and report what
# landed. Two intakes, two failure modes, one tool:
#
#   capture (default)  POST /webhooks/capture/{source} on the dashboard
#                      listener. Token-free, stores the redacted event for
#                      the Event inspector (docs/prds/event-capture-storage.md).
#                      Proves the capture -> inspector half of the Events chain.
#
#   forge (--forge)    POST to [forge].webhook_addr (default port 8645; the
#                      receiver is the root handler of its own server, so any
#                      path works), HMAC-signed with the webhook secret in
#                      X-Hub-Signature-256. The receiver verifies the signature,
#                      parses GitHub's issue event, and queues a task envelope
#                      when the shared dispatch predicate matches. Proves the
#                      forge -> task half. Needs [forge].intake webhook or both,
#                      and forge.webhook_addr set.
#
# Usage:
#   tools/webhook-send.sh                          # one generic capture event
#   tools/webhook-send.sh --kind github-issue      # realistic GitHub delivery
#   tools/webhook-send.sh --kind sentry --source sentry-prod
#   tools/webhook-send.sh --body '{"a":1}'         # an exact payload
#   tools/webhook-send.sh --file /tmp/event.json
#   tools/webhook-send.sh --count 5 --delay 1
#   tools/webhook-send.sh --forge --url http://127.0.0.1:8645 \
#     --secret "$WH_SECRET" --assignee my-bot --label archie
#   tools/webhook-send.sh --list                   # kinds, and exit
#
# --url is the intake a send goes to (the dashboard for capture, the receiver
# for --forge). The read-back always reads the dashboard, which --api points
# elsewhere when the dashboard is not on the default host.
#
# Environment:
#   ARCHIE_URL            dashboard base URL (default http://127.0.0.1:8484)
#   ARCHIE_FORGE_URL      forge receiver base URL, for --forge without --url
#   ARCHIE_WEBHOOK_SECRET the shared secret, for --forge without --secret
#
# Limits the capture intake enforces (defaults from [capture] in
# config.example.toml):
#   rate 1/s per remote address, burst 5  -> a fast --count > 5 needs --delay
#   bodies over 256 KiB are rejected with 413
# Both are per-daemon-configurable, so a 429/413 here is the daemon's policy,
# not a broken sender. The forge intake has no equivalent limits.
set -euo pipefail

BASE_URL="${ARCHIE_URL:-http://127.0.0.1:8484}"
TARGET_URL=""
API_URL=""
SOURCE="manual-test"
KIND="generic"
COUNT=1
DELAY=0
BODY=""
FILE=""
QUIET=0
FORGE=0
FORGE_URL="${ARCHIE_FORGE_URL:-}"
SECRET="${ARCHIE_WEBHOOK_SECRET:-}"
GH_EVENT="issues"
ASSIGNEE=""
LABEL=""

usage() {
  sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
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

Forge mode (--forge) signs the payload for the forge receiver. Only an *issue*
event dispatches, and only when the receiver's dispatch predicate matches, so
reach for --assignee / --label to match a bot-user or label trigger.
KINDS
}

while [ $# -gt 0 ]; do
  case "$1" in
    --source)   SOURCE="${2:?--source needs a value}"; shift 2 ;;
    --kind)     KIND="${2:?--kind needs a value}"; shift 2 ;;
    --count)    COUNT="${2:?--count needs a value}"; shift 2 ;;
    --delay)    DELAY="${2:?--delay needs a value}"; shift 2 ;;
    --body)     BODY="${2:?--body needs a value}"; shift 2 ;;
    --file)     FILE="${2:?--file needs a value}"; shift 2 ;;
    --url)      TARGET_URL="${2:?--url needs a value}"; shift 2 ;;
    --api)      API_URL="${2:?--api needs a value}"; shift 2 ;;
    --forge)    FORGE=1; shift ;;
    --secret)   SECRET="${2:?--secret needs a value}"; shift 2 ;;
    --event)    GH_EVENT="${2:?--event needs a value}"; shift 2 ;;
    --assignee) ASSIGNEE="${2:?--assignee needs a value}"; shift 2 ;;
    --label)    LABEL="${2:?--label needs a value}"; shift 2 ;;
    --quiet)    QUIET=1; shift ;;
    --list)     list_kinds; exit 0 ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; echo >&2; usage >&2; exit 2 ;;
  esac
done

# --url is the intake this send goes to, whichever one that is; the read-back
# always needs the dashboard, which --api names when it is not the same host.
if [ "$FORGE" -eq 1 ]; then
  FORGE_URL="${TARGET_URL:-$FORGE_URL}"
  BASE_URL="${API_URL:-$BASE_URL}"
else
  BASE_URL="${TARGET_URL:-$BASE_URL}"
fi

# A login or label goes into JSON, so it has to be a plain token: anything else
# would be a way to write JSON with the shell rather than a way to send an event.
for value in "$ASSIGNEE" "$LABEL"; do
  case "$value" in
    ""|*[!A-Za-z0-9._-]*) [ -z "$value" ] || { echo "--assignee/--label take plain tokens (A-Za-z0-9._-)" >&2; exit 2; } ;;
  esac
done

CONTENT_TYPE="application/json"
if [ -n "$BODY" ]; then
  PAYLOAD="$BODY"
elif [ -n "$FILE" ]; then
  PAYLOAD="$(cat "$FILE")"
else
  case "$KIND" in
    generic) PAYLOAD='{"hello":"world","n":1}' ;;
    github-issue)
      # The receiver only queues an issue event that is open, not a pull
      # request, and whose action could newly make it eligible.
      assignees="[]"
      [ -n "$ASSIGNEE" ] && assignees="[{\"login\":\"${ASSIGNEE}\"}]"
      labels="[]"
      [ -n "$LABEL" ] && labels="[{\"name\":\"${LABEL}\"}]"
      PAYLOAD="$(printf '{"action":"opened","issue":{"number":42,"title":"Test issue from webhook-send.sh","body":"Filed by tools/webhook-send.sh.","state":"open","html_url":"https://github.com/samcharles93/archie-core/issues/42","assignees":%s,"labels":%s},"repository":{"name":"archie-core","full_name":"samcharles93/archie-core","html_url":"https://github.com/samcharles93/archie-core","owner":{"login":"samcharles93"}},"sender":{"login":"webhook-send"}}' "$assignees" "$labels")"
      ;;
    github-push)
      PAYLOAD='{"ref":"refs/heads/main","repository":{"name":"archie-core","full_name":"samcharles93/archie-core","owner":{"login":"samcharles93"}},"commits":[{"id":"c0ffee1","message":"Test commit from webhook-send.sh"}],"pusher":{"name":"webhook-send"}}'
      ;;
    sentry)
      PAYLOAD='{"action":"created","data":{"issue":{"title":"Test error from webhook-send.sh","culprit":"app/main.go"}},"project":"archie"}'
      ;;
    form)
      CONTENT_TYPE="application/x-www-form-urlencoded"
      PAYLOAD='event=test&source=webhook-send.sh&detail=nothing-to-see'
      ;;
    large)
      PAYLOAD="$(printf '{"filler":"'; head -c 65536 /dev/zero | tr '\0' 'x'; printf '"}')"
      ;;
    empty) PAYLOAD="" ;;
    *) echo "unknown kind: $KIND" >&2; echo >&2; list_kinds >&2; exit 2 ;;
  esac
fi

# sign prints the value the receiver's VerifyHMAC accepts: sha256= followed by
# the hex HMAC-SHA256 of the exact bytes sent. openssl first, python3 second:
# both compute the same digest as internal/webhookguard/hmac.go.
sign() {
  local body="$1"
  if command -v openssl >/dev/null 2>&1; then
    printf '%s' "$body" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $NF}'
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    printf '%s' "$body" | python3 -c 'import hmac,hashlib,sys; print(hmac.new(sys.argv[1].encode(), sys.stdin.buffer.read(), hashlib.sha256).hexdigest())' "$SECRET"
    return
  fi
  echo "signing needs openssl or python3 on PATH" >&2
  return 1
}

send_capture() {
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

send_forge() {
  local url="${FORGE_URL%/}"
  local signature
  signature="sha256=$(sign "$PAYLOAD")"
  local code
  code="$(curl -sS -o /tmp/webhook-send.out -w '%{http_code}' \
    -X POST "$url" \
    -H "Content-Type: ${CONTENT_TYPE}" \
    -H "X-GitHub-Event: ${GH_EVENT}" \
    -H "X-Hub-Signature-256: ${signature}" \
    --data-binary "$PAYLOAD")" || {
    echo "cannot reach ${url} — is [forge].webhook_addr listening?" >&2
    return 1
  }
  # A 2xx means "delivered", not "queued work": the receiver acknowledges
  # anything that verifies and is simply not eligible work. The task read-back
  # below is what tells the two apart.
  if [ "$code" -lt 200 ] || [ "$code" -ge 300 ]; then
    echo "POST ${url} -> ${code}: $(cat /tmp/webhook-send.out)" >&2
    [ "$code" = "401" ] && echo "401 means the signature was refused: check --secret against forge.webhook_secret." >&2
    return 1
  fi
  [ "$QUIET" -eq 1 ] || echo "POST ${url} -> ${code} (X-GitHub-Event: ${GH_EVENT}, signed)"
}

if [ "$FORGE" -eq 1 ]; then
  if [ -z "$FORGE_URL" ]; then
    echo "--forge needs --url or ARCHIE_FORGE_URL: the receiver has an address of its own" >&2
    echo "(watch for \"forge webhook\" in the daemon log for the one it bound)." >&2
    exit 2
  fi
  if [ -z "$SECRET" ]; then
    echo "--forge needs --secret or ARCHIE_WEBHOOK_SECRET: the receiver verifies X-Hub-Signature-256" >&2
    exit 2
  fi
fi

i=0
while [ "$i" -lt "$COUNT" ]; do
  if [ "$FORGE" -eq 1 ]; then send_forge; else send_capture; fi
  i=$((i + 1))
  [ "$i" -lt "$COUNT" ] && [ "$DELAY" != "0" ] && sleep "$DELAY"
done

# Read it back, so a silent drop is visible here rather than only in the UI.
if [ "$QUIET" -eq 0 ]; then
  sleep 0.3
  if [ "$FORGE" -eq 1 ]; then
    echo
    echo "Newest tasks (a signed issue event that matched dispatch shows up here):"
    curl -sS "${BASE_URL%/}/api/tasks?limit=3" | tr ',' '\n' \
      | grep -E '"id"|"status"|"number"|"title"|"repo"' | sed 's/^ *//' | head -16
    [ -z "$ASSIGNEE$LABEL" ] && echo "(no --assignee/--label given: a bot-user or label trigger will not have matched)"
    echo
    echo "Open ${BASE_URL%/}/tasks to see the board."
  else
    echo
    echo "Latest captures:"
    curl -sS "${BASE_URL%/}/api/captures?limit=${COUNT}" \
      | tr ',' '\n' | grep -E '"id"|"source"|"received_at"|"content_type"' | sed 's/^ *//' | head -20
    echo
    echo "Open ${BASE_URL%/}/captures to inspect them."
  fi
fi
