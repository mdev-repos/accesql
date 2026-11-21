#!/usr/bin/env python3
"""Convierte la salida de una terminal (texto con secuencias de color ANSI) en
una imagen SVG con aspecto de ventana de terminal, para usarla en el README.

    python3 scripts/ansi2svg.py entrada.ansi salida.svg [titulo]

Soporta lo que producen lipgloss/bubbletea: colores de 24 bits, de 256 y de 16,
negrita, tenue e invertido. Los bloques (▀ ▄ █ ░) se dibujan como rectángulos
para que el logo salga exacto con cualquier tipografía."""
import re
import sys
from xml.sax.saxutils import escape

DEFAULT_FG, DEFAULT_BG = (205, 214, 244), (30, 30, 46)
CW, CH, FONT = 8.4, 18.0, 14
PAD_X, PAD_TOP, BAR = 18, 14, 34

BASIC = [(0, 0, 0), (205, 49, 49), (13, 188, 121), (229, 229, 16), (36, 114, 200), (188, 63, 188), (17, 168, 205), (229, 229, 229),
         (102, 102, 102), (241, 76, 76), (35, 209, 139), (245, 245, 67), (59, 142, 234), (214, 112, 214), (41, 184, 219), (255, 255, 255)]


def xterm256(n):
    if n < 16:
        return BASIC[n]
    if n >= 232:
        v = 8 + (n - 232) * 10
        return (v, v, v)
    n -= 16
    lv = [0, 95, 135, 175, 215, 255]
    return (lv[n // 36], lv[(n // 6) % 6], lv[n % 6])


def blend(a, b, t):
    return tuple(round(a[i] * (1 - t) + b[i] * t) for i in range(3))


def hexc(c):
    return "#%02x%02x%02x" % c


def parse(text):
    """Devuelve una lista de filas; cada fila es una lista de (carácter, fg, bg, negrita)."""
    rows = []
    fg, bg, bold, faint, rev = None, None, False, False, False
    for line in text.split("\n"):
        row = []
        pos = 0
        for m in re.finditer(r"\x1b\[([0-9;]*)m", line):
            for ch in line[pos:m.start()]:
                row.append((ch, fg, bg, bold, faint, rev))
            pos = m.end()
            p = [int(x) if x else 0 for x in m.group(1).split(";")]
            i = 0
            while i < len(p):
                c = p[i]
                if c == 0:
                    fg, bg, bold, faint, rev = None, None, False, False, False
                elif c == 1: bold = True
                elif c == 2: faint = True
                elif c == 22: bold = faint = False
                elif c == 7: rev = True
                elif c == 27: rev = False
                elif 30 <= c <= 37: fg = BASIC[c - 30]
                elif 90 <= c <= 97: fg = BASIC[c - 90 + 8]
                elif 40 <= c <= 47: bg = BASIC[c - 40]
                elif 100 <= c <= 107: bg = BASIC[c - 100 + 8]
                elif c == 39: fg = None
                elif c == 49: bg = None
                elif c in (38, 48):
                    if i + 1 < len(p) and p[i + 1] == 2 and i + 4 < len(p):
                        col = (p[i + 2], p[i + 3], p[i + 4]); i += 4
                    elif i + 1 < len(p) and p[i + 1] == 5 and i + 2 < len(p):
                        col = xterm256(p[i + 2]); i += 2
                    else:
                        col = None
                    if c == 38: fg = col
                    else: bg = col
                i += 1
        for ch in line[pos:]:
            row.append((ch, fg, bg, bold, faint, rev))
        rows.append(row)
    while rows and not rows[-1]:
        rows.pop()
    return rows


def convert(text, title):
    rows = parse(text)
    cols = max((len(r) for r in rows), default=1)
    w = cols * CW + PAD_X * 2
    h = len(rows) * CH + BAR + PAD_TOP + 12
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{w:.0f}" height="{h:.0f}" viewBox="0 0 {w:.0f} {h:.0f}" role="img" aria-label="{escape(title)}">',
           f'<rect width="{w:.0f}" height="{h:.0f}" rx="10" fill="{hexc(DEFAULT_BG)}"/>',
           f'<path d="M0 10a10 10 0 0 1 10-10h{w-20:.0f}a10 10 0 0 1 10 10v{BAR-10}H0z" fill="#181825"/>',
           '<circle cx="20" cy="17" r="6" fill="#f38ba8"/><circle cx="40" cy="17" r="6" fill="#f9e2af"/><circle cx="60" cy="17" r="6" fill="#a6e3a1"/>',
           f'<text x="{w/2:.0f}" y="22" text-anchor="middle" font-family="sans-serif" font-size="12" fill="#a6adc8">{escape(title)}</text>']
    out.append(f'<g font-family="DejaVu Sans Mono, Menlo, Consolas, Liberation Mono, monospace" font-size="{FONT}" xml:space="preserve">')
    for r, row in enumerate(rows):
        y0 = BAR + PAD_TOP + r * CH
        base = y0 + CH - 5
        cells = []
        for ch, fg, bg, bold, faint, rev in row:
            f, b = fg or DEFAULT_FG, bg
            if rev:
                f, b = (b or DEFAULT_BG), (fg or DEFAULT_FG)
            if faint:
                f = blend(f, b or DEFAULT_BG, 0.5)
            cells.append((ch, f, b, bold))
        # fondos: tramos continuos del mismo color
        c = 0
        while c < len(cells):
            b = cells[c][2]
            if b is None:
                c += 1
                continue
            e = c
            while e < len(cells) and cells[e][2] == b:
                e += 1
            out.append(f'<rect x="{PAD_X + c * CW:.1f}" y="{y0:.1f}" width="{(e - c) * CW + 0.4:.1f}" height="{CH + 0.4:.1f}" fill="{hexc(b)}"/>')
            c = e
        # Los bloques se dibujan como rectángulos y el texto palabra por palabra,
        # cada una en su columna exacta: así la cuadrícula no depende de la
        # tipografía ni de cómo el navegador trate los espacios.
        word, word_start, word_key = [], 0, None

        def flush():
            nonlocal word, word_key
            if word:
                text = escape("".join(word))
                weight = ' font-weight="bold"' if word_key[1] else ""
                if len(word) == 1:  # un solo símbolo: centrado en su celda
                    x = PAD_X + (word_start + 0.5) * CW
                    out.append(f'<text x="{x:.1f}" y="{base:.1f}" fill="{hexc(word_key[0])}"{weight} text-anchor="middle">{text}</text>')
                else:
                    x = PAD_X + word_start * CW
                    out.append(f'<text x="{x:.1f}" y="{base:.1f}" fill="{hexc(word_key[0])}"{weight} textLength="{len(word) * CW:.1f}" lengthAdjust="spacingAndGlyphs">{text}</text>')
            word, word_key = [], None

        for c, (ch, f, b, bold) in enumerate(cells):
            x = PAD_X + c * CW
            if ch in "▀▄█░▒▓":
                flush()
                col = hexc(f)
                if ch == "▀": out.append(f'<rect x="{x:.1f}" y="{y0:.1f}" width="{CW + 0.4:.1f}" height="{CH / 2 + 0.2:.1f}" fill="{col}"/>')
                elif ch == "▄": out.append(f'<rect x="{x:.1f}" y="{y0 + CH / 2:.1f}" width="{CW + 0.4:.1f}" height="{CH / 2 + 0.4:.1f}" fill="{col}"/>')
                elif ch == "█": out.append(f'<rect x="{x:.1f}" y="{y0:.1f}" width="{CW + 0.4:.1f}" height="{CH + 0.4:.1f}" fill="{col}"/>')
                else:
                    op = {"░": 0.25, "▒": 0.5, "▓": 0.75}[ch]
                    out.append(f'<rect x="{x:.1f}" y="{y0:.1f}" width="{CW + 0.4:.1f}" height="{CH + 0.4:.1f}" fill="{col}" fill-opacity="{op}"/>')
                continue
            if ch == " ":
                flush()
                continue
            key = (f, bold)
            if word and key != word_key:
                flush()
            if not word:
                word_start, word_key = c, key
            word.append(ch)
        flush()
    out.append("</g></svg>")
    return "\n".join(out) + "\n"


if __name__ == "__main__":
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    title = sys.argv[3] if len(sys.argv) > 3 else "accesql"
    with open(sys.argv[1], encoding="utf-8") as f:
        svg = convert(f.read(), title)
    with open(sys.argv[2], "w", encoding="utf-8") as f:
        f.write(svg)
