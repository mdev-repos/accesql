#!/usr/bin/env bash
# Genera los recursos de Windows del ejecutable (ícono, versión y manifiesto) como
# cmd/accesql/rsrc_windows_amd64.syso. Go lo enlaza solo al compilar para Windows.
#
#   scripts/winres.sh [versión]      # por defecto, la de internal/version
#
# Necesita go-winres; se descarga la primera vez (hace falta red). El resultado
# se versiona, así que compilar no lo requiere: esto se corre al cambiar el ícono
# o la versión (scripts/build.sh lo hace por vos).
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-$(sed -n 's/^var Version = "\(.*\)"/\1/p' internal/version/version.go)}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "versión inválida: '$VERSION' (se espera X.Y.Z)" >&2; exit 1; }
go run github.com/tc-hib/go-winres@v0.3.3 make \
  --in packaging/windows/winres.json --arch amd64 \
  --out cmd/accesql/rsrc \
  --product-version "$VERSION.0" --file-version "$VERSION.0"
echo "cmd/accesql/rsrc_windows_amd64.syso (versión $VERSION)"
