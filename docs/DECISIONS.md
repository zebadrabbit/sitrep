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
- **Network graph panels** (owner request, 2026-09-11): rates are kept per interface, 600 samples (20 min) instead of 60, so a wide terminal can fill two samples per cell. The mirrored graph draws from the left and scrolls once full; right-anchoring left a 2-minute history stranded at the far right of a 170-column frame. `enter` graphs the selected interface, `space` adds or removes it as a side-by-side panel (cap 4). Histories live in the module, not `Data`, so `--json` stays small.
- **README screenshots without a browser** (2026-09-11): `make shots` renders `--once --demo` frames through `scripts/ansi2svg.py` (every word and braille dot absolutely positioned) and ImageMagick into `docs/img/*.png`. vhs captures nothing on pandalab and charm `freeze` segfaults rasterizing; ImageMagick's own SVG renderer is fine once nothing relies on whitespace or font fallback. `--once --keys j,enter` presses keys before rendering so a shot can show the detail pane; the network module synthesizes traffic in `--demo`, since the fixture has one counter sample.
- **Release plumbing** (2026-09-11): `.github/workflows/ci.yml` runs lint, tests and a static build on push and PR; `release.yml` runs goreleaser on a `v*` tag, so a release is `git tag v1.0.0 && git push --tags`. `scripts/redact-fixtures.py` now scrubs MACs (`02:00:00:00:00:NN`) and its IPv6 regex accepts a single leading group, which is why EUI-64 link-locals had slipped through. No LICENSE file yet: that is the owner's choice and the one thing blocking a public release.


## 2026-09-11 — Borrowed from syswatch

Reviewed matthart1983/syswatch and kernwatch for ideas. What HANDOFF already
took from syswatch (lite/dense sizes, mirrored network graph, the key set,
honest needs-root) stays. Parked as v2: session recording and timeline
scrubbing (§1 History), a settings key and command palette (no settings
screen), and everything kernwatch does with eBPF, cgroups, and actions (§2).

- **Insights + `sitrep why`** (syswatch's Insights tab and `why`). `internal/insight` is pure
  functions over the Data types already collected: memory and swap, load per cpu, mounts past
  the `ui.Bar` thresholds, SMART failed, failed units and timers, unhealthy containers, reboot
  required, security updates, an hour of errors, unidentified listeners. Overview shows the top
  three under the status line; `why` prints them all and exits 1 on crit. Not a module: it has
  no collector, and a rule that needs new data belongs in the module that collects it. System
  Data grew a `cpus` field for the load rule.
- **`sitrep diff old.json [new.json]`** (syswatch's `diff`). Compares a saved snapshot to the
  box now: listeners new or gone (folded across addresses like the Ports tab), containers and
  their state, unit states, sessions, mounts and usage past 5 points, kernel, reboot, pending
  updates. Still no history (§1): the file is the user's, written by `snapshot`, kept wherever
  they like. Exit 0 always; `why` is the health check.
- **PSI on System** (syswatch's pressure vitals): `/proc/pressure/{cpu,memory,io}` some avg10,
  a fourth card line. Stalls, not utilisation, so warn at 25 and crit at 50, and an insight
  rule for memory pressure.
- **`f` freezes the screen** (syswatch's pause, kernwatch's freeze). Results are dropped and
  tickers stop while frozen; `f` again restarts every collector. `p` was taken by the probe.
- **`terminal` theme** (syswatch): ANSI indexes instead of hex, so the palette is the
  emulator's. `lipgloss.Color` already parses them; no loader change. No selection background,
  index 0 is unreadable on a light scheme.
- **Cells colored by their own height** (syswatch). The CPU sparkline uses the Bar thresholds
  per cell (ok, warn ≥ 85, crit ≥ 95); the mirrored network graph dims columns under a quarter
  of peak and keeps the direction color above, because throughput is not an error state.
- **CPU topology + per-cpu grid on System** (owner asked for "an htop vibe"). `lscpu -J` once for
  model/sockets/cores/threads/MHz and the NUMA cpu lists; node memory from sysfs each tick;
  per-cpu % from gopsutil. The grid (`  0 ██░░░░░░░░ 16%` cells, wrapped to width, one block per
  node) is used when it fits the height the header and top-5 tables leave; otherwise one
  sparkline cell per cpu per node, so a quad-socket EPYC still fits on one screen.
- **Hostname, firewall and NIC hardware on Network** (owner). Firewall = first active of
  ufw / nftables / firewalld via `systemctl show` (unprivileged); rules via `ufw status verbose`
  or `nft list ruleset` only when root, `◐ rules need root` otherwise, never an error. firewalld
  rules are not read (nothing to capture from). NIC model from `lspci -mm -nn` once, matched by
  PCI address; speed/duplex/driver from sysfs each tick; virtual ifaces stay blank.
- **Samba server block** (owner: "not just shares but important config items"). `testparm -sv`
  replaces `-s`: one exec, every effective value. [global] is allowlisted to ~20 keys the block
  shows; the 480-key dump is not a snapshot's business. `vfs objects` / fruit deliberately out —
  owner's call: a module of their own if wanted (docs/MODULES.md). Version from `smbd --version`
  when smbstatus is refused. Daemon states via the shared `collect.ShowProps`.

