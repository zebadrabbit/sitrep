#!/usr/bin/env python3
"""Render a `sitrep --once` frame (truecolor ANSI on stdin) to SVG on stdout.

Every styled run is an absolutely positioned <text>, so ImageMagick's own SVG
renderer (no librsvg, no browser) rasterizes it faithfully:

    CLICOLOR_FORCE=1 COLORTERM=truecolor sitrep --once --demo --size 118x34 ports \\
      | scripts/ansi2svg.py | convert -density 144 svg:- docs/img/ports.png

ponytail: SGR subset sitrep emits (reset, bold, faint, underline, strike,
16/256/truecolor fg+bg). Braille is drawn as dots, since DejaVu Sans Mono lacks U+28xx.
"""
import re
import sys
from xml.sax.saxutils import escape

FONT, BRAILLE_FONT = "DejaVu Sans Mono", "FreeMono"
SIZE = 13.0
CW, LH = SIZE * 0.602, SIZE * 1.35  # DejaVu Sans Mono advance is 1233/2048 em
PAD, RADIUS = 18, 10
BG, FG, BORDER = "#000000", "#c8c8c8", "#262626"
BASIC = ["#000000", "#cc3333", "#7cb342", "#f5a623", "#4d7fd0", "#b48ead", "#4dd0e1", "#c8c8c8",
         "#6b6b6b", "#e53935", "#9ccc65", "#ffd54f", "#64b5f6", "#ce93d8", "#80deea", "#ffffff"]
SGR = re.compile(r"\x1b\[([0-9;]*)m")


def c256(n):
    if n < 16:
        return BASIC[n]
    if n >= 232:
        v = 8 + (n - 232) * 10
        return "#%02x%02x%02x" % (v, v, v)
    n -= 16
    r, g, b = n // 36, n // 6 % 6, n % 6
    return "#%02x%02x%02x" % tuple(0 if c == 0 else 55 + c * 40 for c in (r, g, b))


def apply(st, params):
    codes = [int(p or 0) for p in params.split(";")] if params else [0]
    i = 0
    while i < len(codes):
        c = codes[i]
        if c == 0:
            st = {}
        elif c == 1:
            st["bold"] = True
        elif c == 2:
            st["faint"] = True
        elif c == 4:
            st["ul"] = True
        elif c == 9:
            st["strike"] = True
        elif c == 22:
            st.pop("bold", None); st.pop("faint", None)
        elif c == 24:
            st.pop("ul", None)
        elif c == 29:
            st.pop("strike", None)
        elif c in (38, 48):
            key = "fg" if c == 38 else "bg"
            if i + 1 < len(codes) and codes[i + 1] == 2 and i + 4 < len(codes):
                st[key] = "#%02x%02x%02x" % tuple(codes[i + 2:i + 5]); i += 4
            elif i + 1 < len(codes) and codes[i + 1] == 5 and i + 2 < len(codes):
                st[key] = c256(codes[i + 2]); i += 2
        elif c == 39:
            st.pop("fg", None)
        elif c == 49:
            st.pop("bg", None)
        elif 30 <= c <= 37:
            st["fg"] = BASIC[c - 30]
        elif 90 <= c <= 97:
            st["fg"] = BASIC[c - 90 + 8]
        elif 40 <= c <= 47:
            st["bg"] = BASIC[c - 40]
        elif 100 <= c <= 107:
            st["bg"] = BASIC[c - 100 + 8]
        i += 1
    return st


def cells(line):
    """Yield (char, style) per visible cell."""
    st, pos = {}, 0
    for m in SGR.finditer(line):
        for ch in line[pos:m.start()]:
            yield ch, dict(st)
        st = apply(st, m.group(1))
        pos = m.end()
    for ch in line[pos:]:
        yield ch, dict(st)


def runs(line):
    """Group cells into runs of identical style and font."""
    out = []
    for ch, st in cells(line):
        if ch in "\r\x1b":
            continue
        st["font"] = BRAILLE_FONT if 0x2800 <= ord(ch) <= 0x28FF else FONT
        if out and out[-1][1] == st:
            out[-1][0] += ch
        else:
            out.append([ch, st])
    return out


def braille(text, x, y, fill):
    """Draw braille cells as dots: font-independent, pixel-exact.
    Bit layout per U+2800: col0 = 0x01 0x02 0x04 0x40, col1 = 0x08 0x10 0x20 0x80 (top→bottom)."""
    bits = [(0x01, 0, 0), (0x02, 0, 1), (0x04, 0, 2), (0x40, 0, 3), (0x08, 1, 0), (0x10, 1, 1), (0x20, 1, 2), (0x80, 1, 3)]
    r, dx, dy = CW / 5.2, CW / 2, LH / 4
    out = []
    for i, ch in enumerate(text):
        v = ord(ch) - 0x2800
        for bit, c, rrow in bits:
            if v & bit:
                out.append(f'<circle cx="{x + i * CW + dx * (c + 0.5):.2f}" cy="{y + dy * (rrow + 0.5):.2f}" r="{r:.2f}" fill="{fill}"/>')
    return out


def main():
    lines = sys.stdin.read().split("\n")
    while lines and not SGR.sub("", lines[-1]).strip():
        lines.pop()
    cols = max(len(SGR.sub("", l)) for l in lines) if lines else 80
    w, h = cols * CW + 2 * PAD, len(lines) * LH + 2 * PAD
    o = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{w:.0f}" height="{h:.0f}" font-family="{FONT}" font-size="{SIZE}">',
         f'<rect x="0.5" y="0.5" width="{w - 1:.0f}" height="{h - 1:.0f}" rx="{RADIUS}" fill="{BG}" stroke="{BORDER}"/>']
    for row, line in enumerate(lines):
        y = PAD + row * LH
        col = 0
        for text, st in runs(line):
            x = PAD + col * CW
            if "bg" in st:
                o.append(f'<rect x="{x:.2f}" y="{y:.2f}" width="{len(text) * CW:.2f}" height="{LH:.2f}" fill="{st["bg"]}"/>')
            attrs = [f'fill="{st.get("fg", FG)}"']
            if st["font"] != FONT:
                attrs.append(f'font-family="{st["font"]}"')
            if st.get("bold"):
                attrs.append('font-weight="bold"')
            if st.get("faint"):
                attrs.append('fill-opacity="0.55"')
            deco = [d for d, k in (("underline", "ul"), ("line-through", "strike")) if st.get(k)]
            if deco:
                attrs.append(f'text-decoration="{" ".join(deco)}"')
            if st["font"] == BRAILLE_FONT:
                o.extend(braille(text, x, y, st.get("fg", FG)))
                col += len(text)
                continue
            # One <text> per word: ImageMagick collapses whitespace inside text.
            for m in re.finditer(r"\S+", text):
                o.append(f'<text x="{x + m.start() * CW:.2f}" y="{y + SIZE:.2f}" {" ".join(attrs)}>{escape(m.group())}</text>')
            col += len(text)
    o.append("</svg>")
    sys.stdout.write("\n".join(o) + "\n")


if __name__ == "__main__":
    main()
