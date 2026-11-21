#!/usr/bin/env bash
# Regenera las capturas del menú de terminal (docs/img/tui-*.svg).
# Requiere testdb/Northwind.accdb y Python 3.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
ACCESQL_SCREENSHOTS="$tmp" go test ./internal/tui/app -run '^TestScreenshots$' -count=1 >/dev/null
mkdir -p docs/img
for f in "$tmp"/*.ansi; do
  name="$(basename "$f" .ansi)"
  python3 scripts/ansi2svg.py "$f" "docs/img/$name.svg" "accesql"
  echo "docs/img/$name.svg"
done
