#!/bin/sh
# Instala Accesql para tu usuario (no hace falta sudo): el programa, el ícono y el
# lanzador del menú de aplicaciones.
#
#   ./install.sh                  instala en ~/.local
#   ./install.sh --prefix /usr/local   (como root) instala para todos
#   ./install.sh --uninstall      desinstala
set -eu

PREFIX="${HOME}/.local"
UNINSTALL=0
while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) [ $# -ge 2 ] || { echo "falta el valor de --prefix" >&2; exit 2; }; PREFIX="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    -h|--help) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "opción desconocida: $1 (probá --help)" >&2; exit 2 ;;
  esac
done

BIN="$PREFIX/bin/accesql"
DESKTOP="$PREFIX/share/applications/accesql.desktop"
ICON_SVG="$PREFIX/share/icons/hicolor/scalable/apps/accesql.svg"
ICON_PNG="$PREFIX/share/icons/hicolor/256x256/apps/accesql.png"

refresh() {
  command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$PREFIX/share/applications" >/dev/null 2>&1 || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q -t "$PREFIX/share/icons/hicolor" >/dev/null 2>&1 || true
}

if [ "$UNINSTALL" -eq 1 ]; then
  rm -f "$BIN" "$DESKTOP" "$ICON_SVG" "$ICON_PNG"
  refresh
  echo "Accesql desinstalado de $PREFIX"
  exit 0
fi

HERE="$(cd "$(dirname "$0")" && pwd)"
for f in accesql accesql.desktop accesql.svg accesql.png; do
  [ -f "$HERE/$f" ] || { echo "falta $f junto a install.sh" >&2; exit 1; }
done

install -d "$PREFIX/bin" "$PREFIX/share/applications" "$PREFIX/share/icons/hicolor/scalable/apps" "$PREFIX/share/icons/hicolor/256x256/apps"
install -m 755 "$HERE/accesql" "$BIN"
install -m 644 "$HERE/accesql.svg" "$ICON_SVG"
install -m 644 "$HERE/accesql.png" "$ICON_PNG"
# El lanzador apunta a la ruta absoluta, así funciona aunque ~/.local/bin no esté en el PATH.
sed "s|^Exec=.*|Exec=\"$BIN\"|" "$HERE/accesql.desktop" > "$DESKTOP"
chmod 644 "$DESKTOP"
refresh

echo "Accesql instalado en $PREFIX"
echo "  programa:  $BIN"
echo "  lanzador:  $DESKTOP (buscá «Accesql» en el menú de aplicaciones)"
case ":$PATH:" in
  *":$PREFIX/bin:"*) ;;
  *) echo "Aviso: $PREFIX/bin no está en tu PATH; para usarlo desde la terminal agregalo o ejecutá $BIN" ;;
esac
