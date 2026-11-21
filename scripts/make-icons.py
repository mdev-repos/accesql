#!/usr/bin/env python3
"""Genera los íconos de la aplicación a partir del logo (internal/web/static/logo.svg):

    packaging/icons/accesql.ico   ícono de Windows (16 a 256 px)
    packaging/icons/accesql.png   ícono de 256 px para Linux
    packaging/icons/accesql.svg   copia del logo

Requiere Chrome/Chromium (para dibujar el SVG con fondo transparente) y Pillow.
El resultado se versiona: no hace falta correr esto para compilar."""
import os, shutil, subprocess, sys, tempfile
from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SVG = os.path.join(ROOT, "internal", "web", "static", "logo.svg")
OUT = os.path.join(ROOT, "packaging", "icons")
SIZES = [16, 24, 32, 48, 64, 128, 256]

def chrome():
    for name in ("google-chrome", "google-chrome-stable", "chromium", "chromium-browser"):
        p = shutil.which(name)
        if p:
            return p
    sys.exit("no encontré Chrome ni Chromium")

def render(browser, size, path):
    """Dibuja el logo a size×size px con fondo transparente. Se pide a Chrome
    una ventana más grande y se recorta, porque la ventana mínima ronda los 500 px."""
    html = f'<!doctype html><body style="margin:0;background:transparent"><img src="file://{SVG}" width="{size}" height="{size}" style="display:block">'
    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False) as f:
        f.write(html)
    try:
        subprocess.run([browser, "--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
                        "--default-background-color=00000000", f"--screenshot={path}",
                        "--window-size=600,600", f"file://{f.name}"], check=True, capture_output=True)
    finally:
        os.unlink(f.name)
    Image.open(path).convert("RGBA").crop((0, 0, size, size)).save(path)

def main():
    os.makedirs(OUT, exist_ok=True)
    browser = chrome()
    with tempfile.TemporaryDirectory() as tmp:
        frames = []
        for s in SIZES:
            p = os.path.join(tmp, f"{s}.png")
            render(browser, s, p)
            frames.append(Image.open(p).convert("RGBA"))
        big = frames[-1]
        alpha = big.getchannel("A").getextrema()
        if alpha == (255, 255):
            sys.exit("el ícono salió sin transparencia: revisá la versión de Chrome")
        big.save(os.path.join(OUT, "accesql.png"))
        # Pillow arma el .ico reduciendo la imagen sobre la que se llama, por eso se
        # parte de la más grande.
        big.save(os.path.join(OUT, "accesql.ico"), format="ICO", sizes=[(s, s) for s in SIZES])
    shutil.copyfile(SVG, os.path.join(OUT, "accesql.svg"))
    for n in ("accesql.ico", "accesql.png", "accesql.svg"):
        print(os.path.join("packaging", "icons", n), os.path.getsize(os.path.join(OUT, n)), "bytes")

main()
