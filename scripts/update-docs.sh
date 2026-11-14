#!/usr/bin/env bash
set -Eeuo pipefail
# Called locally by an administrator or the protected deployment runner.
set +x
INSTALL_DIR="${INSTALL_DIR:-/opt/open-ai-canvas}"
SOCKET="${CANVAS_UPDATER_SOCKET:-/run/open-ai-canvas-updater/updater.sock}"
action="${1:-status}"
case "$action" in status|check|update|rollback) ;; *) printf 'Usage: update-docs.sh status|check|update|rollback [digest] [expected-current]\n' >&2; exit 2 ;; esac
command -v jq >/dev/null || { printf 'jq is required\n' >&2; exit 2; }
token="$(sed -n 's/^CANVAS_UPDATER_TOKEN=//p' "$INSTALL_DIR/.env" | tail -n 1)"
[[ ${#token} -ge 32 ]] || { printf 'Host Updater token missing\n' >&2; exit 2; }
[[ -S "$SOCKET" ]] || { printf 'Host Updater socket missing\n' >&2; exit 2; }
# Keep the token out of command arguments and process listings.
request() {
    local method="$1" path="$2" body="${3:-}"
    printf 'header = "Authorization: Bearer %s"\n' "$token" |
        curl --config - --fail-with-body --silent --show-error --max-time 180 \
          --unix-socket "$SOCKET" -X "$method" -H 'Content-Type: application/json' \
          --data-binary "$body" "http://localhost/v1/docs/$path"
}
case "$action" in
    status) request GET status; exit ;;
    check) request POST check '{}'; exit ;;
    update)
        [[ -n "${2:-}" && -n "${3:-}" ]] || { printf 'digest and expected-current are required\n' >&2; exit 2; }
        body="$(jq -nc --arg target "$2" --arg current "$3" '{targetDigest:$target,expectedCurrentDigest:$current}')"
        request POST update "$body" >/dev/null ;;
    rollback)
        [[ -n "${2:-}" && -n "${3:-}" ]] || { printf 'expected-current and reason are required\n' >&2; exit 2; }
        body="$(jq -nc --arg current "$2" --arg reason "$3" '{expectedCurrentDigest:$current,reason:$reason}')"
        request POST rollback "$body" >/dev/null ;;
esac
for ((attempt=0;attempt<240;attempt++)); do
    status="$(request GET status)"
    phase="$(jq -r '.operation.phase' <<<"$status")"
    case "$phase" in
        succeeded) printf '%s\n' "$status"; exit 0 ;;
        failed|rolled_back|manual_intervention) printf '%s\n' "$status"; exit 1 ;;
    esac
    sleep 2
done
printf 'Timed out; check docs status before retrying\n' >&2
exit 1
