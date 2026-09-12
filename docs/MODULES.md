# Writing your own module

sitrep is compiled-in modules behind one Go interface. There is no plugin
protocol (HANDOFF §1), so a new tab is a new package under `internal/modules/`
and one line in the registry. This walks through one end to end, using a
Samba VFS/`fruit` tab as the running example since it is the kind of thing
the shipped Samba module deliberately leaves out.

The contract lives in `docs/HANDOFF.md`: §4.1 is the interface, §7 the UI
rules, §10 the invariants. Read those first; this page is the how.

## What a module ships with

Not done without all six (CLAUDE.md):

1. a **collector** that gets the data,
2. a **parser** as a pure function over bytes,
3. a **fixture** captured from real output and redacted,
4. a **parser test** against that fixture,
5. `Card` and `View`, and
6. the `modules info` text.

## 1. The package

```
internal/modules/smbvfs/
    smbvfs.go
    smbvfs_test.go
testdata/fixtures/smbvfs/
    testparm_sv.txt
```

`internal/module/module.go` is the interface. A minimal module:

```go
// Package smbvfs shows the VFS stack per Samba share.
package smbvfs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

type Share struct {
	Name  string            `json:"name"`
	VFS   []string          `json:"vfs"`             // vfs objects, in stack order
	Fruit map[string]string `json:"fruit,omitempty"` // fruit:* keys
}

// Data is what Collect returns; JSON tags feed `sitrep snapshot` and --json.
type Data struct {
	Shares []Share `json:"shares"`
}

type Module struct {
	run  *collect.Runner
	demo bool
}

func New(demo bool) *Module { return &Module{run: collect.New("smbvfs", demo), demo: demo} }

func (*Module) ID() string              { return "smbvfs" }
func (*Module) Title() string           { return "SMB VFS" }
func (*Module) Flags() module.Flags     { return module.Flags{ApplianceSensitive: true} }
func (*Module) Interval() time.Duration { return 30 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: vfs objects and fruit:* settings per share.
Needs:    testparm (samba). No root.
Execs:    testparm -sv.
Interval: 30s.`
}

func (*Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("testparm") {
		return module.Availability{State: module.Missing, Reason: "testparm missing (samba)"}
	}
	return module.Availability{State: module.Available, Reason: "testparm"}
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	res, err := m.run.Run(ctx, "testparm", "-sv")
	if err != nil {
		return nil, err
	}
	return Data{Shares: Parse(res.Stdout)}, nil
}
```

Rules that matter here:

- **Every exec goes through `collect.Runner`.** It applies the timeout,
  records the duration for `doctor`, captures fixtures, and serves them in
  `--demo`. No `exec.Command` in a module. Host files go through
  `run.ReadFile` and `run.Readlink` for the same reasons.
- **`Detect` never fails the app.** Missing binary → `Missing` with the
  package name in the reason. Needs root → `NeedsRoot`, and the fields you
  cannot fill show `◐` (HANDOFF §10.4). Errors are for things that should
  have worked.
- **Wrap errors** with the module name: `fmt.Errorf("smbvfs: parse: %w", err)`.
- **Respect `ctx`.** The runner does for execs; loops over many pids or
  files should check `ctx.Err()`.

## 2. The parser

A pure function from bytes to your `Data`, no I/O, so the test can feed it
the fixture:

```go
// Parse reads testparm -sv and keeps vfs objects and fruit:* per share.
func Parse(out []byte) []Share { … }
```

Exported so tests and `doctor` can reach it. Keep it tolerant: skip lines
you do not understand, never panic on garbage, and return an error only for
input that is not the format at all.

## 3. Fixture and test

Capture from the live box, then scrub:

```
make build
SITREP_CAPTURE_FIXTURES=1 ./dist/sitrep smbvfs
python3 scripts/redact-fixtures.py
```

The runner writes `testdata/fixtures/smbvfs/testparm_sv.txt` (name from
`collect.FixtureName`: command, args joined with `_`, dashes dropped). Check
the file yourself for anything private that the redactor does not know
about, such as usernames in paths. Fixtures are embedded at build time, so
rebuild before `--demo` will see a new one.

The test reads it through the embedded FS:

```go
func TestParse(t *testing.T) {
	b, err := sitrep.Fixtures.ReadFile("testdata/fixtures/smbvfs/testparm_sv.txt")
	if err != nil {
		t.Fatal(err)
	}
	sh := Parse(b)
	if len(sh) == 0 || sh[0].VFS == nil {
		t.Fatalf("got %+v", sh)
	}
}
```

Add one that renders the demo data too: `New(true).Collect(ctx)` then
`View(d, 100, 30)` and `Card(d, 60)`, checking a token or two. That is the
cheapest integration test of the whole render path.

## 4. Card and View

`Card` is the Overview contribution: at most 6 lines and 3 primary numbers.
`View` is the tab; `h == 0` means unbounded (one-shot output), otherwise cut
to `h` lines. Width is `w`.

```go
func (*Module) Card(d module.Data, w int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	return fmt.Sprintf("%d shares with a vfs stack", len(sd.Shares))
}

func (*Module) View(d module.Data, w, h int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	rows := [][]string{}
	for _, sh := range sd.Shares {
		rows = append(rows, []string{sh.Name, strings.Join(sh.VFS, " → ")})
	}
	return ui.Table([]string{"SHARE", "VFS"}, rows, w)
}
```

UI rules that reviewers will hold you to:

- Colors come from `theme.Current()` only. If you are typing
  `lipgloss.Color("#`, stop (HANDOFF §10.5).
- Status glyphs are `theme.Current().Glyph` (`● ○ ◐ ! !! +`), nothing else.
- Widgets in `internal/ui`: `Table`, `Bar`, `Sparkline`, `Mirror`, `Box`,
  `Columns`, `Cursor`. Use them before writing your own.
- A `View` over ~80 lines gets split into helpers.
- Tab-local keys go in `Keys()` (they show in the footer) and are handled in
  `Update`. No settings screens; configuration is CLI.
- Read-only. No actions, ever, in v1 (HANDOFF §1).

## 5. Register it

`internal/modules/registry.go`:

```go
module.Register(smbvfs.New(demo))
```

Registry order is tab order; hotkeys are assigned from it. Add the tab to the
list in the `screenshot` Makefile target so `make screenshot` renders it.

If your module reads another module's data (Ports reads Docker's port map),
declare it in `Flags().After` so one-shot paths collect in the right order.

## 6. Prove it

```
make lint test screenshot
./dist/sitrep doctor          # must be clean; ◐ is fine, errors are not
sudo ./dist/sitrep doctor
./dist/sitrep --demo          # style against the fixture, not the live box
./dist/sitrep smbvfs --json   # the JSON others will script against
```

Then a `modules info smbvfs` read-through, and a line in
`docs/DECISIONS.md` if you made a call someone might question. Commit with
the unprivileged `doctor` output in the message.

## Where things go wrong

- Parser test passes, live tab is empty: the collector is using a different
  flag than the fixture was captured with, or `Detect` said `Missing`.
  `sitrep doctor` shows both.
- `--demo` does not show your fixture: you did not rebuild after capturing.
- Works with `sudo`, blank without: mark the fields `◐ needs root`, do not
  return an error. See how Samba handles `smbstatus`.
- Slow collector: `doctor` warns past 500ms. Raise `Interval()`, cache what
  is static (System runs `lscpu` once), or set `Flags().Slow`.
