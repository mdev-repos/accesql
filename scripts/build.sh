#!/usr/bin/env bash
# Compila Accesql para Linux y Windows y arma los paquetes para publicar.
#
#   scripts/build.sh                  versión de internal/version/version.go
#   VERSION=0.2.0 scripts/build.sh    otra versión (queda dentro del programa y en el .exe)
#   TARGETS="linux/amd64" scripts/build.sh     solo algunos destinos
#
# Resultado en dist/:
#   accesql-<versión>-linux-amd64.tar.gz     programa + instalador + lanzador + ícono + licencias
#   accesql-<versión>-windows-amd64.zip      accesql.exe (con ícono y datos de versión) + licencias
#   SHA256SUMS                               huellas para verificar las descargas
#
# Todo es Go puro (CGO_ENABLED=0), así que se puede compilar para Windows desde Linux.
# Para el ícono de Windows, scripts/winres.sh necesita red la primera vez (descarga go-winres).
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(sed -n 's/^var Version = "\(.*\)"/\1/p' internal/version/version.go)}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "versión inválida: '$VERSION' (se espera X.Y.Z)" >&2; exit 1; }
TARGETS="${TARGETS:-linux/amd64 windows/amd64}"
DIST=dist
STAGE="$DIST/.stage"

echo "== Accesql $VERSION"
rm -rf "$DIST"
mkdir -p "$STAGE"

# Avisos de terceros al día con las dependencias actuales (y control de copyleft).
python3 scripts/gen-notices.py >/dev/null
# Recursos de Windows (ícono, versión, manifiesto) con la versión que se compila.
case " $TARGETS " in *" windows/"*) scripts/winres.sh "$VERSION" ;; esac

build() { # goos goarch outfile
  GOOS="$1" GOARCH="$2" CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X accesql/internal/version.Version=$VERSION" -o "$3" ./cmd/accesql
}

common() { # carpeta destino: licencias que van en todos los paquetes
  cp LICENSE NOTICE THIRD_PARTY_NOTICES.md "$1/"
  cp -r LICENSES "$1/LICENSES"
}

# Fecha fija para que el mismo código dé el mismo paquete (reproducible).
EPOCH="$(git log -1 --format=%ct 2>/dev/null || date +%s)"

for target in $TARGETS; do
  goos="${target%/*}"; goarch="${target#*/}"
  name="accesql-$VERSION-$goos-$goarch"
  dir="$STAGE/$name"
  mkdir -p "$dir"
  echo "-- $target"
  case "$goos" in
    linux)
      build linux "$goarch" "$dir/accesql"
      cp packaging/linux/install.sh packaging/linux/accesql.desktop "$dir/"
      cp packaging/icons/accesql.svg packaging/icons/accesql.png "$dir/"
      sed "s/@VERSION@/$VERSION/g" packaging/linux/LEEME.txt > "$dir/LEEME.txt"
      common "$dir"
      chmod 755 "$dir/accesql" "$dir/install.sh"
      tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$EPOCH" \
          -C "$STAGE" -czf "$DIST/$name.tar.gz" "$name"
      ;;
    windows)
      build windows "$goarch" "$dir/accesql.exe"
      sed "s/@VERSION@/$VERSION/g" packaging/windows/LEEME.txt | sed 's/$/\r/' > "$dir/LEEME.txt"
      common "$dir"
      # zip guarda la fecha de cada entrada (carpetas incluidas) y el orden en que
      # las recibe: se fijan las fechas y se ordena la lista para que sea reproducible.
      find "$dir" -exec touch -d "@$EPOCH" {} +
      (cd "$STAGE" && find "$name" | LC_ALL=C sort | zip -q -X -@ "../$name.zip")
      ;;
    *) echo "destino no soportado: $target" >&2; exit 1 ;;
  esac
done

rm -rf "$STAGE"
(cd "$DIST" && sha256sum $(ls | grep -E '\.(tar\.gz|zip)$') > SHA256SUMS)
echo
ls -la "$DIST" | awk 'NR>3 {printf "%10d  %s\n", $5, $9}'
