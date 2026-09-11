# Decisions

One line each, with the reason. HANDOFF.md is the contract; this file records what the implementer was allowed to decide (§12) and any deviation with its reason.

## 2026-09-10 — pre-flight

- **Bubble Tea v2** (`bubbletea/v2` v2.0.9, `lipgloss/v2` v2.0.6, `bubbles/v2` v2.2.1). v2 has been stable since early 2026 with nine patch releases; v1 (1.3.10, Sept 2025) is maintenance-only. Don't mix v1 and v2 packages.
- **Go toolchain 1.25 via `go.mod`.** The Charm v2 packages require Go ≥ 1.25. `/usr/local/go` on pandalab is 1.22.2; `GOTOOLCHAIN=auto` (the default) downloads the newer toolchain into `~/go/pkg/mod` on first build. No system install needed.
- **TOML: `github.com/BurntSushi/toml`.** Stdlib has no TOML. Smallest, best-known, no transitive deps.
- **gopsutil/v4** for CPU/mem/load/procs per HANDOFF §4.3. Cgo-free on Linux.

## v2 (ideas parked here, not built)

- Actions (`systemctl restart`, mounts) — v1 is read-only by contract.
- **Run `~/update-all.sh` from the Updates tab** (owner asked 2026-09-10). It does apt full-upgrade, docker pulls and service restarts: an action by any reading of §1. v1 shows what it would do and how the last run went; a `u` key that runs it belongs here.
- `sitrep serve` over SSH via Wish.

## 2026-09-10 — Phase 1

- **Module interface deltas from HANDOFF §4.1:** dropped `Hotkey()` (registry assigns it; a method invites hardcoding), added `Update(tea.Msg) tea.Cmd` (tab-local keys had no receiver; the probe needs an async Cmd), added `Info() string` (`modules info` needed a source).
- **Fixture naming:** `testdata/fixtures/<module>/<cmd>_<args>.txt` with dashes stripped (`ss_tulnpH.txt`), host files under `<module>/fs/<path>`, symlink targets as `<path>.link`. One module runs the same binary with different args, so `<cmd>.txt` alone was ambiguous. `scripts/redact-fixtures.py` scrubs LAN/public IPs, hostname and user after capture.
- **Demo clock:** in `--demo` the ports module's "now" is the fixture's `btime + /proc/uptime`, so AGE is stable in screenshots instead of growing forever.
- **Socket-activated listeners:** when `ss -p` lists systemd (pid 1) alongside the service, the service is the owner. Lowest pid otherwise, so nginx shows its master, not a worker.
- **Ports USER column** is dropped when the content area is under 90 columns (full layout at exactly 100×30, and lite). The detail pane always has it. The alternative was clipping IDENTITY, which is the column that matters.
- **Cold start** in `doctor` is measured by exec'ing `sitrep --once --demo`: real process start to a full frame, not an in-process timer.
- **System demo fixture** is the collected `Data` as JSON (`system/data.json`) because gopsutil reads /proc directly; there is no command output to capture. Ports goes through the real parsers.
- **Ports grouping (owner request, 2026-09-10):** rows sharing process name and port fold across addresses and IP families into one leader line (`0.0.0.0:137 +16`), collapsed by default. `space` expands one group in place, `c` shows the flat list. Grouping is by process *name*, not pid, because Docker runs one docker-proxy per IP family. tcp and udp never fold together since CONN only means something for tcp. Not in HANDOFF §6; adds to it without removing anything.
- **Updates module pulled forward from v1.5** (owner request, 2026-09-10). Read-only: pending apt (security flagged), reboot-required, running vs newest installed kernel, and the last run of the owner's update script parsed from its log (`updates_log`, default `~/update-all.log`). No snap or pip "outdated" checks: those query the network, and the probe is sitrep's only outbound traffic.

## 2026-09-10 — Phase 4

- **nord theme, `theme set`, `config edit`.** `config edit` is the one `exec` outside `collect.Run`: it launches the user's `$EDITOR` on the user's config file, which is not collection.
- **Network without iproute2** reads names and counters from `/proc/net/dev`, no addresses or routes. Found by running `doctor` in bare `debian:stable-slim`; invariant 4 needs Network to show.
- **README gif not committed.** `docs/demo.tape` and `make gif` are in; on pandalab vhs 0.12 captures zero frames (headless Chromium starts, ttyd runs, no screenshots land), so the gif is rendered elsewhere or not at all. Not worth more of the lab box's time.

## 2026-09-11 — Selection background

- **Cursor row paints a background** (owner request, 2026-09-11): theme key `selection`, `#060606` in amber and nord, off in mono. Amends HANDOFF §7 "never paint a background": the row under the accent bar is the one exception, in tables and the sidebar alike, through `ui.Cursor`. A background over a row of colored cells is cut short by each inner reset, so `ui.Cursor` re-arms it after every `ESC[m`.

