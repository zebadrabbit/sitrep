# Theming

sitrep ships four themes and reads yours from `~/.config/sitrep/themes/<name>.toml`.
Pick one with `sitrep theme set <name>` or `theme = "<name>"` in `config.toml`.
`sitrep theme list` shows what is available.

```
sitrep theme list        # amber bbs mono nord terminal + anything in ~/.config/sitrep/themes/
sitrep theme set nord
```

## The file

```toml
name   = "amber"
accent = "#F5A623"   # wordmark, selection bar, hotkey characters
ok     = "#7CB342"   # ● and healthy bars
warn   = "#F5A623"   # ◐ ! and 85% bars
crit   = "#E53935"   # ○ !! and 95% bars
dim    = "#6B6B6B"   # secondary text, borders, inactive rows
port   = "#4DD0E1"   # port numbers in the Ports tab
selection = "#060606" # cursor-row background; the only one sitrep paints
ascii  = false       # true swaps ● ○ ◐ ▸ for * o ~ >
```

Every color is a hex string or an ANSI index (`"1"`..`"255"`); `terminal`
uses indexes 1–8 so the colors are whatever your emulator's scheme says they
are. Leave one empty (`""`) for the terminal's default foreground; `mono` does that for all of them and relies on bold and
faint. There is no page background key on purpose: sitrep leaves your
terminal's. `selection` is the one background it paints, under the cursor
row next to the accent bar; empty turns it off.

A user file with the same name as a builtin overrides it. Partial files are
fine: unspecified keys fall back to the terminal default, not to the builtin,
so copy the builtin first if you only want to change one color:

```
mkdir -p ~/.config/sitrep/themes
sitrep theme show > /dev/null   # just to make sure it runs
cp $(go env GOMODCACHE 2>/dev/null)/... # or grab internal/theme/themes/amber.toml from the repo
```

## Glyphs

The status glyph set is fixed and part of the design, not the theme:

```
●  ok / confident      ○  failed / stopped / unknown      ◐  degraded / needs root / likely
!  warning threshold   !! critical threshold              +  new since launch
```

`ascii = true` maps them to `* o ~ ! !! +` for terminals whose font lacks
them. `sitrep doctor` warns when the locale is not UTF-8 and suggests it.

## Rules for contributors

- Colors come from `internal/theme` only. A `lipgloss.Color("#…")` anywhere
  else is a bug (HANDOFF §10.5).
- Hotkey characters use `theme.Hotkey`, via `ui.Hotkey(key, rest)`. Nothing
  else uses that style (§10.6).
- Adding a color means adding a key to `Theme`, a style to `Styles`, and a
  value in all four builtin files.

## Colors look wrong over ssh?

Hex themes need a truecolor terminal. ssh does not forward `COLORTERM`, so a
terminal that speaks truecolor still shows up on the box as `xterm-256color`,
and every hex color is snapped to the nearest of 256. Nord's green (`#A3BE8C`)
lands on khaki, which is why its charts and bars look tan. Fix it on the box:

```
echo 'export COLORTERM=truecolor' >> ~/.bashrc
```

Or use the `terminal` theme, which uses your emulator's own palette by index
and needs no truecolor at all.

