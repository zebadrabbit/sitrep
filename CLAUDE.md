# sitrep

Read-only terminal situation report for a Linux box. Go + Bubble Tea. Single static binary.

**Read `docs/HANDOFF.md` before doing anything.** It is the contract: locked decisions (§1), non-goals (§2), module interface (§4.1), the Ports spec (§6), UI spec (§7), invariants (§10), build phases (§11). If a request contradicts §1 or §10, stop and ask instead of complying.

## Environment

- You are running **on the target box** (`pandalab`, Debian-family, systemd, Docker, Samba, Tailscale). Whatever you build, you can run immediately. Do it — never assume a collector works because its parser test passed.
- Verify both ways every time: `./dist/sitrep doctor` unprivileged (the default experience, must be clean with `◐` markers, never errors) and `sudo ./dist/sitrep doctor`.
- Keep Linux-specific code behind `//go:build linux` with stubs so the tree compiles elsewhere. Cheap hygiene.
- `sitrep --demo` runs from `testdata/fixtures/`. Use it for UI work so the screen doesn't change under you while you're styling it.
- You'll see your own ssh session, the Claude Code tunnel, and any dev servers in Ports and Sessions. That's expected and a useful live test — don't filter them out.
- Go toolchain: latest stable. `CGO_ENABLED=0` always.

## Working rules

- One module per PR-sized change. Each module ships with: collector, parser, fixture(s), parser tests, `Card`, `View`, `modules info` text. Not done without all six.
- Capture fixtures from real output (`SITREP_CAPTURE_FIXTURES=1 sitrep snapshot` on pandalab), redact hostnames/IPs, commit them.
- Colors come from `internal/theme` only. If you're typing `lipgloss.Color("#` in a view, stop.
- Hotkey highlight: `theme.Hotkey` on the key character only. Same everywhere.
- Status glyphs: `● ○ ◐ ! !! +` — nothing else, no emoji.
- Collectors respect `ctx`. Every exec goes through `collect.Run`. No raw `exec.Command` in modules.
- Never add an action (start/stop/restart/mount/write). v1 is read-only. Put the idea in `docs/DECISIONS.md` under "v2" and move on.
- Never add a settings screen to the TUI. Configuration is CLI (`sitrep modules …`, `sitrep config …`).
- New dependency? Justify it in `docs/DECISIONS.md` in one line. Prefer stdlib + gopsutil + Charm.
- Before finishing a phase: `make lint test screenshot` green, then paste the unprivileged `doctor` output into the commit message.
- Never run anything that changes this host's state. You're on a live server. Read-only applies to your shell commands too — no `apt install` without asking, no `systemctl` beyond `status`/`list-units`.

## Commands

```
make build         # dist/sitrep (static, CGO_ENABLED=0), plus arm64
make demo          # go run ./cmd/sitrep --demo
make test          # go test ./... (no root, no network, < 10s)
make screenshot    # renders every tab via --once --demo to docs/screens/
make lint
make install       # ~/.local/bin/sitrep
```

## Layout

```
cmd/sitrep/          cobra entry
internal/app/        Bubble Tea root model, layout modes, key routing
internal/module/     Module interface + registry
internal/collect/    exec wrapper (timeout, fixture capture), /proc helpers
internal/detect/     appliance / init / container runtime detection
internal/config/     TOML, acks, enabled modules
internal/theme/      theme.toml → lipgloss styles
internal/ui/         table, card, sparkline, mirrored braille graph, glyphs
internal/modules/    one package per module
testdata/fixtures/   captured command output, per module
docs/                HANDOFF.md, DECISIONS.md, THEMING.md, demo.tape
```

## Style

- Errors are values; wrap with `%w` and context (`"ports: parse ss: %w"`).
- No global state except the module registry and theme.
- Keep functions short; a `View` over ~80 lines gets split into widgets.
- Comments explain *why*. The HANDOFF explains *what*.
