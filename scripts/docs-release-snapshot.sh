#!/usr/bin/env bash
set -Eeuo pipefail
SNAPSHOT_TEMP="$(mktemp -d)"
trap 'rm -f "$SNAPSHOT_TEMP/docs-image.json" "$SNAPSHOT_TEMP/error" "$SNAPSHOT_TEMP/labels.json" "$SNAPSHOT_TEMP/canonical.json"; rmdir "$SNAPSHOT_TEMP"' EXIT
snapshot_output="${DOCS_SNAPSHOT_OUTPUT:-dist/docs-image.json}"
snapshot_owner="${GITHUB_REPOSITORY_OWNER,,}"
snapshot_script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
snapshot_assets="$(gh release view "$GITHUB_REF_NAME" --repo "$GITHUB_REPOSITORY" --json assets --jq '.assets | map(.name)' 2>"$SNAPSHOT_TEMP/error")" || {
    grep -Fxq 'release not found' "$SNAPSHOT_TEMP/error" || { cat "$SNAPSHOT_TEMP/error" >&2; exit 1; }
    snapshot_assets='[]'
}
snapshot_exists=false
if printf '%s' "$snapshot_assets" | node -e 'const fs=require("node:fs");process.exit(JSON.parse(fs.readFileSync(0,"utf8")).includes("docs-image.json")?0:1)'; then
    snapshot_exists=true
    gh release download "$GITHUB_REF_NAME" --repo "$GITHUB_REPOSITORY" --pattern docs-image.json --dir "$SNAPSHOT_TEMP"
    node "$snapshot_script/docs-release-metadata.mjs" --validate "$SNAPSHOT_TEMP/docs-image.json" "$snapshot_owner" > "$SNAPSHOT_TEMP/canonical.json"
fi
case "${1:?prepare or publish required}" in
    prepare)
        mkdir -p "$(dirname "$snapshot_output")"
        if "$snapshot_exists"; then
            cp "$SNAPSHOT_TEMP/canonical.json" "$snapshot_output"
        else
            image="ghcr.io/$snapshot_owner/open-ai-canvas-docs"
            digest="$(docker buildx imagetools inspect "$image:docs-main" --format '{{.Manifest.Digest}}')"
            [[ "$digest" =~ ^sha256:[a-f0-9]{64}$ ]]
            docker pull "$image@$digest"
            docker image inspect "$image@$digest" --format '{{json .Config.Labels}}' > "$SNAPSHOT_TEMP/labels.json"
            node "$snapshot_script/docs-release-metadata.mjs" "$image@$digest" "$SNAPSHOT_TEMP/labels.json" "$snapshot_owner" "$snapshot_output"
        fi ;;
    publish)
        node "$snapshot_script/docs-release-metadata.mjs" --validate "$snapshot_output" "$snapshot_owner" > "$SNAPSHOT_TEMP/labels.json"
        if "$snapshot_exists"; then
            cmp -s "$SNAPSHOT_TEMP/canonical.json" "$SNAPSHOT_TEMP/labels.json" || { echo 'Published docs snapshot differs; refusing to overwrite' >&2; exit 1; }
        else
            gh release upload "$GITHUB_REF_NAME" "$snapshot_output" --repo "$GITHUB_REPOSITORY"
        fi ;;
    *) exit 2 ;;
esac
rm -f "$SNAPSHOT_TEMP/docs-image.json" "$SNAPSHOT_TEMP/error" "$SNAPSHOT_TEMP/labels.json" "$SNAPSHOT_TEMP/canonical.json"
rmdir "$SNAPSHOT_TEMP"
trap - EXIT
