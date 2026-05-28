#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

cat > "$TMP_DIR/package.json" <<'JSON'
{
  "type": "module",
  "dependencies": {
    "browser-snapshot": "file:/Users/wanglei/Projects/github-flaboy/browser-snapshot"
  }
}
JSON

(
  cd "$TMP_DIR"
  npm install
  npx browser-snapshot export-runtime > "$TMP_DIR/runtime.js"
)

{
  printf 'package browser\n\n'
  printf 'const browserSnapshotRuntimeScript = `'
  sed 's/`/` + "`" + `/g' "$TMP_DIR/runtime.js"
  printf '`\n'
} > "$SCRIPT_DIR/browser_snapshot_runtime.go"
