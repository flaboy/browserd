#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

SOURCE_REV=3f0544fa1f0cba8dd053cd0414b8704f22de8128
if [[ -n "${BROWSER_SNAPSHOT_SOURCE:-}" ]]; then
  (
    cd "$BROWSER_SNAPSHOT_SOURCE"
    npm run build
    node dist/cli.js export-runtime > "$TMP_DIR/runtime.js"
  )
  SOURCE_REV="local runtime sha256:$(shasum -a 256 "$TMP_DIR/runtime.js" | awk '{print $1}')"
else
cat > "$TMP_DIR/package.json" <<'JSON'
{
  "type": "module",
  "dependencies": {
    "browser-snapshot": "git+https://github.com/flaboy/browser-snapshot.git#3f0544fa1f0cba8dd053cd0414b8704f22de8128"
  }
}
JSON

(
  cd "$TMP_DIR"
  npm install
  npx browser-snapshot export-runtime > "$TMP_DIR/runtime.js"
)
fi

{
  printf 'package browser\n\n'
  printf '// Generated from browser-snapshot %s; DO NOT EDIT.\n' "$SOURCE_REV"
  printf 'const browserSnapshotRuntimeScript = `'
  sed 's/`/` + "`" + `/g' "$TMP_DIR/runtime.js"
  printf '`\n'
} > "$SCRIPT_DIR/browser_snapshot_runtime.go"
