# sitrep — Design Hand-off

**One line:** a read-only terminal situation report for a Linux box. Open it first, learn what the machine is doing and whether it's healthy, close it. Depth lives in btop and netwatch; sitrep is about *identity and status*, not graphs.

**Status:** design locked. This document is the contract. Deviations need a reason written into `DECISIONS.md`.

---

## 1. Locked decisions

| Decision | Value | Why |
|---|---|---|
| Name / binary | `sitrep` | Situation report. One word. Sits naturally next to `btop` and `netwatch`. |
| Language | Go 1.23+ | Single static binary (`CGO_ENABLED=0`), scp to any box, no runtime. |
| TUI stack | Bubble Tea, Lip Gloss, Bubbles (latest stable; check whether v2 is stable before scaffolding and pick one — don't mix) | Charm ecosystem; owner already knows it. |
| CLI | `cobra` | Standard subcommand layout; completions for free. |
| Config | TOML at `$XDG_CONFIG_HOME/sitrep/config.toml` (default `~/.config/sitrep/`) | Human-editable; `sitrep config` subcommands wrap it. |
| v1 mode | **Strictly read-only.** No actions. Not even `systemctl restart`. | Keeps the security and testing surface small; actions are a v2 tier. |
| Privileges | Unprivileged by default. Show `◐ needs root` on the exact fields that are missing. `sudo sitrep` or `setcap` unlocks. No helper daemon, no polkit, no Landlock. | Honest and zero infrastructure. |
| Targets | Any Linux with a `/proc`. systemd is the primary service backend; boxes without it still run with the Services module marked "unsupported init". | Best effort everywhere it can run. |
| Appliances | Detected (TrueNAS SCALE, Proxmox, Unraid, Synology DSM, OpenWrt). Modules flagged `appliance_sensitive` are *off* until acknowledged once via CLI. Never asked again. | Owner's call: warn once, then trust them. |
| Module management | **CLI only.** The TUI has no settings screens. | Keeps the UI clean. |
| Mouse | None. | Not needed. Keyboard-only, hotkeys highlighted consistently. |
| Extensibility | Compiled-in modules behind one Go interface. **No plugin protocol in v1.** | A JSON-over-stdout plugin system eats a week for nothing. |
| History | None beyond a per-module in-memory ring buffer (60 samples) for sparklines. No database, no export. | Prometheus exists. |
| Theme | `theme.toml`; shipped default is amber accent. Colors are never hardcoded in views. | Branding lives in one file. |

## 2. Non-goals (say no when tempted)

- Editing anything: config files, users, cron, mounts, firewall.
- Packet capture, L7 decoding, connection baselining/alerting → that's `netwatch`.
- Per-core CPU graphs, process trees, kill → that's `btop`.
- Multi-host / agents / central dashboard → that's netdata. (v2 idea: `sitrep serve` over SSH via Wish. Not now.)
- Non-Linux. BSD/macOS collectors are out of scope; the build may compile there but nothing is promised.
- A web UI. Ever.

## 3. CLI surface

```
sitrep                      # launch the TUI (full mode)
sitrep --lite               # single 80x24 screen, no sidebar
sitrep --view dense         # zero-chrome multi-box layout for big terminals (min 130x44)
sitrep --demo               # run against bundled fixture data; no root, no host access
sitrep --once [module]      # render one frame to stdout and exit (for scripts / screenshots)

sitrep doctor               # check for problems (see §8)
sitrep snapshot [-o json]   # dump every enabled module's current data as JSON to stdout
sitrep <module>             # one-shot table for that module, non-TUI (e.g. `sitrep ports`)

sitrep modules list         # id, state (enabled/disabled/undetected/needs-ack), detect reason
sitrep modules enable <id>  # prompts y/N once if appliance_sensitive on an appliance; records ack
sitrep modules disable <id>
sitrep modules info <id>    # what it collects, what it needs, what it shells out to

sitrep config path|show|init|edit
sitrep theme list|show|set <name>
sitrep version              # version, commit, go version, build date
sitrep completion <shell>
```

Every subcommand that prints a table honors `--json`. `snapshot` and `--once` are what Claude Code uses to test collectors without driving the TUI.

## 4. Architecture

```
cmd/sitrep/            main.go — cobra root, subcommands
internal/app/          Bubble Tea root model: layout modes, sidebar, tab routing, key handling
internal/module/       Module interface, registry, Availability, Card, lifecycle
internal/collect/      exec helpers (timeout, fixture capture), /proc readers, gopsutil wrappers
internal/detect/       appliance + init-system + container-runtime detection
internal/config/       TOML load/save, acks, enabled set, theme selection
internal/theme/        theme.toml loader, Lip Gloss style set, amber default
internal/ui/           shared widgets: table, card, sparkline, mirrored braille graph, status glyphs
internal/modules/
    overview/  system/  ports/  services/  disks/  network/  docker/  samba/  nfs/  sessions/  logs/
testdata/fixtures/     captured command output per module (see §9)
docs/                  HANDOFF.md (this), DECISIONS.md, THEMING.md
```

### 4.1 Module interface

```go
type Availability struct {
    State  AvailState // Available | Degraded | NeedsRoot | Missing | NeedsAck | Unsupported
    Reason string     // human sentence, shown in sidebar tooltip and `modules list`
}

type Module interface {
    ID() string                     // "ports"
    Title() string                  // "Ports"
    Hotkey() rune                   // '3' — assigned by registry order, not hardcoded
    Flags() ModuleFlags             // ApplianceSensitive, Slow, NeedsRootForFull
    Detect(ctx context.Context, env detect.Env) Availability
    Interval() time.Duration        // per-module refresh
    Collect(ctx context.Context) (Data, error)   // runs off the UI goroutine, MUST respect ctx
    Card(d Data, w int) string      // ≤ 6 lines; the module's contribution to Overview
    View(d Data, w, h int) string   // full tab
    Keys() []key.Binding            // tab-local keys, merged into footer
}
```

Rules:

- `Collect` never touches the UI. The app runs one goroutine per module on a ticker at `Interval()`, with `context.WithTimeout(ctx, 2s)` (Slow-flagged modules get 10s). Results arrive as a `DataMsg{ModuleID, Data, Err, Took}`.
- A failed collection keeps the previous data on screen and shows `!` + the error in the tab header. The UI never shows a blank tab because one command hung.
- `Card` shows the **three numbers that matter** for that module. Not a mini-tab. Discipline here is what makes the overview look designed.
- Modules are registered in `internal/modules/registry.go` in display order. Hotkeys `1..9,0` follow that order over *enabled* modules only.

### 4.2 Data flow

```
ticker(module) → Collect(ctx) → DataMsg → app.Update → store[moduleID] → View on next frame
```

First paint happens **before** any collection completes: every tab renders a "collecting…" skeleton from zero data. Target: first frame < 150 ms on a cold start.

### 4.3 Collection strategy

Shell out; don't reimplement. Every exec goes through `collect.Run(ctx, name, args...)` which: applies the timeout, captures stdout/stderr, records duration (surfaced in `doctor`), and — when `SITREP_CAPTURE_FIXTURES=1` — writes the raw output to `testdata/fixtures/<module>/<cmd>.txt`.

| Need | Source |
|---|---|
| CPU, memory, load, uptime, disk usage, net counters, processes | `gopsutil/v4` |
| Listening + established sockets | `ss -tulnpH` / `ss -tunapH` (fallback: `/proc/net/{tcp,tcp6,udp,udp6}` + `/proc/*/fd` scan when `ss` missing) |
| Process start time (port "listening since") | `/proc/<pid>/stat` starttime × clock ticks vs boot time |
| Services | `systemctl list-units --type=service --all --output=json`, `systemctl list-units --failed --output=json` |
| Block devices, mounts | `lsblk -J -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL`, `/proc/mounts`, `findmnt -J` |
| SMART | `smartctl -H -j /dev/X` (root only, Slow, 5 min interval) |
| Network | gopsutil interfaces + `ip -j addr`, `ip -j route`, `/etc/resolv.conf` / `resolvectl status` |
| Docker | `docker ps --format '{{json .}}'` + `docker inspect` for port maps; Podman via the same CLI shape if `docker` is absent and `podman` present |
| Samba | `testparm -s` (shares), `smbstatus -j` (clients, locks) |
| NFS | `exportfs -v`, `/proc/fs/nfsd/clients/*` (server); `/proc/mounts` type nfs (client) |
| Sessions | `who`, `loginctl list-sessions --output=json` |
| Logs | `journalctl -p err -n 50 --output=json --no-pager` |
| Appliance detection | `/etc/os-release`, presence of `/etc/version` (TrueNAS), `/etc/pve` (Proxmox), `/boot/config/ident.cfg` (Unraid), `/etc/synoinfo.conf` (Synology), `/etc/openwrt_release` |

## 5. Modules — v1 set and priority

Build in this order. Each is shippable alone.

| # | Module | Interval | Card (overview) | Tab |
|---|---|---|---|---|
| 1 | **overview** | — | — | Composed from every enabled module's `Card`. Header: hostname, distro, kernel, uptime, load, `● n ok ○ n failed ◐ n needs-root`. |
| 2 | **ports** (hero) | 3s | count listening, count established, newest listener | See §6. |
| 3 | **system** | 2s | load, mem bar, 60s CPU sparkline | Distro/kernel/arch, uptime, load 1/5/15, CPU% sparkline, mem/swap bars, top 5 processes by CPU and by RSS. That's it — btop does the rest. |
| 4 | **services** | 5s | n active, n failed, most recent failure | Failed units pinned to top with exit code and time since. Then active, then inactive (collapsed). Filter with `/`. |
| 5 | **disks** | 30s / SMART 5m | worst-full mount %, n devices, SMART worst | Block tree from `lsblk`, usage bars per mount, `!` at ≥85% and `!!` at ≥95`, SMART health column when root. Mounts are here, not a separate module. |
| 6 | **network** | 2s | primary IP, ↓↑ rate, default route iface | Interfaces with addrs, state, rx/tx rate (mirrored braille graph), default route, DNS servers, Tailscale/WireGuard ifaces labeled as such. |
| 7 | **docker** | 5s | n running, n unhealthy/exited | Containers: name, image, status, uptime, published ports. Also feeds `ports` for `docker-proxy` resolution. Podman fallback. |
| 8 | **samba** | 10s | n shares, n connected clients | Shares (path, browseable, writable), connected sessions, open files. `appliance_sensitive`. |
| 9 | **nfs** | 10s | n exports, n clients | Exports with options, connected clients; NFS client mounts. `appliance_sensitive`. |
| 10 | **sessions** | 5s | n users logged in | `who` + loginctl, remote IP, idle. |
| 11 | **logs** | 10s | n errors last hour | Last 50 `journalctl -p err`, unit-colored. |

v1.5 candidates (don't start until the above are done): firewall (ufw/nftables read), updates (`apt list --upgradable` cached hourly), zfs (`zpool status -j`), gpu (`nvidia-smi --query-gpu`), temps (gopsutil sensors).

## 6. Ports — the hero module

This is the reason the tool exists. The owner leaves things running and wants to know **what they are, how long they've been there, and roughly how they're doing.**

### Row (list view)

```
PROTO  ADDR:PORT           PROCESS            USER    AGE     CONN  IDENTITY
tcp    0.0.0.0:22          sshd (1042)        root    12d 4h   2    ssh
tcp    0.0.0.0:445         smbd (2210)        root    12d 4h   1    samba
tcp    0.0.0.0:1337        docker▸planka      root    3d 2h    0    planka (http)
tcp    127.0.0.1:11434     ollama (8871)      winter  6h 12m   0    ollama api
tcp6   [::]:8096           docker▸jellyfin    root    3d 2h    3    jellyfin (http)
udp    0.0.0.0:41641       tailscaled (988)   root    12d 4h   —    tailscale
tcp    0.0.0.0:3000        node (30112)       winter  41m      0    ? node — likely dev server (grafana? planka?)
```

- **AGE** = process start time (from `/proc/<pid>/stat`). This is the honest proxy — the kernel does not expose socket age. Label the column "AGE" and explain in `modules info ports` that it's the owning process's age. If the pid is unknown (needs root), show `◐`.
- **CONN** = established connections to that local port, counted from `ss -tunaH`. UDP shows `—`.
- **IDENTITY** resolution order, first hit wins, but show the confidence glyph:
  1. Docker port map → `docker▸<container>` and the container's image name as identity. (`●` confident)
  2. Process name match against `internal/modules/ports/identify.go` table (`sshd`→ssh, `smbd`→samba, `ollama`, `tailscaled`, `postgres`, `redis-server`, `nginx`, `caddy`, …). (`●`)
  3. Port number match against a curated table (32400 plex, 8096 jellyfin, 9000 portainer, 3000 grafana/dev, 8080 generic http, 5432 postgres, 6379 redis, 1883 mqtt, 11434 ollama, 4533 navidrome, 8123 home-assistant, 9090 prometheus, 3001 uptime-kuma, 8384 syncthing, 22000 syncthing-sync, 2283 immich, 5000 generic/registry, …). Ship at least 150 entries; keep as a TOML file embedded via `embed` so users can override with `~/.config/sitrep/ports.toml`. (`◐` likely)
  4. `/etc/services` name. (`◐`)
  5. `?` + process name + a heuristic hint (node/python on 3000-9000 → "likely dev server"). (`○` unknown)
- Sort: default by port; `s` cycles port / age / conn / process. `/` filters. `L` toggles loopback-only listeners (hidden by default? **No** — shown, but dimmed; the owner specifically wants to find forgotten things).
- New listeners since sitrep started get a `+` marker for 60s. Listeners that vanish get a strikethrough for one refresh, then drop.

### Detail pane (`enter` on a row)

- Full cmdline, cwd, exe path, pid/ppid, user, systemd unit owning the pid (`/proc/<pid>/cgroup`), container if any.
- Process CPU% and RSS (gopsutil), threads, open fds count.
- Established peers for this port: remote addr, state, age — top 10.
- Identity sources tried and which one hit.
- `p` — **probe** (opt-in, per keypress, never automatic): TCP connect to `127.0.0.1:<port>`; if identity says http-ish, `HEAD /` with a 1s timeout and show status line + `Server:` header; otherwise show the first 128 bytes of banner if any arrive within 1s. This is the only outbound network activity sitrep ever performs, and it's only to localhost, only on demand. Document that.

### Lite / dense mode

In `--lite`, ports gets the biggest box on the single screen. In dense, ports takes a full column.

## 7. UI spec

### Layout modes

- **full** (default): left sidebar with numbered tabs + hotkey highlight, content area, one-line footer with context keys. Minimum 100×30; below that, auto-fall to lite with a one-time hint.
- **lite** (`--lite` or narrow terminal): no sidebar; tabs shown as a single header line `[1]Overview [2]Ports …`; fits 80×24.
- **dense** (`--view dense`): no chrome; 2×2 or 3×2 boxes of user-chosen modules (`dense_modules` in config); min 130×44; refuse smaller with a message.

### Keys (global, always the same)

```
1-9,0   jump to tab        tab / shift-tab   next / prev tab
j/k ↑/↓ move selection     enter             open detail        esc   back
/       filter             s                 cycle sort         r     force refresh
?       help overlay       q                 quit
```

Tab-local keys are listed in the footer after a `│` separator. Hotkeys are rendered with the **accent color on the key character only**, e.g. in the sidebar "3 Ports" the `3` is amber; in the footer `r`efresh is amber on the `r`. Same style object everywhere (`theme.Hotkey`). Nothing else uses that style.

### Status glyphs — the only ones allowed

```
●  ok / confident      ○  failed / stopped / unknown      ◐  degraded / needs root / likely
!  warning threshold   !! critical threshold              +  new since launch
```

No emoji. No spinners beyond a 4-frame braille spinner in the header while collecting.

### Branding

- Header wordmark: `sitrep` in accent, then `▸ <hostname>` in normal text. That's the whole logo. No ASCII art banner (it costs 5 lines everywhere; put it in the README gif instead).
- Default theme `amber`: accent `#F5A623`, ok `#7CB342`, warn `#F5A623`, crit `#E53935`, dim `#6B6B6B`, fg terminal default, bg terminal default (the cursor row's `selection` `#060606` is the only background ever painted — everything else respects the user's terminal).
- Ship 3 themes: `amber` (default), `mono` (no color; bold/dim only, for `TERM=dumb`/screenshots), `nord`-ish. Theme file documented in `docs/THEMING.md`.
- README gets a `vhs` tape (`docs/demo.tape`) rendering `sitrep --demo` so the gif is reproducible.

## 8. `sitrep doctor`

Prints a checklist and exits non-zero if anything is `○`. Categories:

1. **Environment** — Linux + `/proc` present, terminal size and `TERM`, color support (truecolor/256/none), locale supports the glyph set (fallback to ASCII glyphs if not — `theme.ascii = true`).
2. **Privileges** — euid, effective capabilities, whether `setcap` grants exist on the binary; prints the exact `setcap` line to run for full view without sudo.
3. **Config** — path, parses, unknown keys warned, acks list.
4. **Detection** — appliance / init system / container runtime, and what that disabled.
5. **Modules** — for each: Availability state + reason; missing binaries with the package name hint (`smbstatus` → `samba`); collector dry-run timing (`ports 41ms`, `services 180ms`, `smart skipped: needs root`). Warn on anything > 500ms with a suggestion (raise its interval).
6. **Ports table** — how many identities came from each source; count of `?` unknowns.

## 9. Testing rules

- Every collector has a parser test against a captured fixture in `testdata/fixtures/<module>/`. Fixtures are real output captured with `SITREP_CAPTURE_FIXTURES=1`, redacted of anything private (replace real hostnames/IPs with `example`/`10.0.0.x`).
- Parsers must handle: empty output, the binary missing (exec error), a timeout, and one malformed line without dropping the rest.
- `--demo` uses the same fixtures through the same parsers, so demo mode is also an integration test of the render path.
- `go test ./...` runs without root and without network in < 10s.
- A `make screenshot` target renders each tab via `--once --demo` at 100×30 and 80×24 to `docs/screens/` so layout regressions are visible in PRs.

## 10. Invariants (write these into `CLAUDE.md`, enforce in review)

1. The UI goroutine never blocks on I/O. All collection is async with a context deadline.
2. No collector runs faster than its `Interval()`; no global tick faster than 1s.
3. Read-only. The only syscalls that touch anything outside the config dir are reads, execs of read-only commands, and the on-demand localhost probe.
4. Missing capability is displayed, never fatal. `sitrep` on a box with nothing installed but `/proc` still starts and shows System + Ports + Network + Sessions.
5. Colors only from `theme`. Views never construct a `lipgloss.Color` directly.
6. Hotkey highlighting uses one style, everywhere.
7. Static binary: `CGO_ENABLED=0`, no cgo deps (gopsutil is fine without cgo on Linux).
8. Startup to first frame < 150 ms. Measure it in `doctor`.
9. Cards are ≤ 6 lines and show ≤ 3 primary numbers.
10. No new module without a fixture, a parser test, and an entry in `modules info`.

## 11. Build phases for Claude Code

Each phase ends with something runnable and a commit. Verify with `sudo ./dist/sitrep doctor` and an unprivileged run on pandalab before moving on — both must be clean, since unprivileged is the default experience.

**Phase 0 — scaffold (½ day)**
Repo, `go.mod` (`github.com/zebadrabbit/sitrep`), cobra root with `version`, `doctor` (env + privileges + config sections only), `config init/path/show`. Bubble Tea app that paints the header, empty sidebar, footer, and handles `q`/`?`. Theme loader with `amber` and `mono`. `--demo` flag plumbed (no data yet). `Makefile`: `build` (static, linux/amd64 + arm64), `run`, `demo`, `test`, `screenshot`, `lint` (golangci-lint with `errcheck`, `govet`, `staticcheck`), `install` (copy to `~/.local/bin`).

**Dev environment note:** development happens directly on the primary target (`pandalab`, Debian-family, Docker + Samba + Tailscale present) over ssh. That means the box being monitored is the box being developed on — the dev server, the ssh session, and the Claude Code tunnel will all show up in Ports, which is a free live test. Keep live collectors behind `//go:build linux` anyway (cheap, and keeps the tree honest if it's ever opened on a laptop), but verification is just `make build && sudo ./dist/sitrep doctor`.

**Phase 1 — module system + overview + system + ports (2 days)**
`Module` interface, registry, async collection loop with `DataMsg`, first-paint skeleton. Overview composed from cards. System module. **Ports module complete per §6**, including identity tables, age, conn counts, detail pane, and the opt-in probe. Fixtures + tests. `sitrep ports --json` and `sitrep snapshot`. This phase is the MVP — if the project stopped here it would still be useful.

**Phase 2 — services, disks, network, docker (2 days)**
Docker feeds Ports' `docker▸` resolution. Mirrored braille graph widget lands with Network. SMART behind root. `doctor` grows the Modules and Ports sections.

**Phase 3 — samba, nfs, sessions, logs + appliance gating (1 day)**
Appliance detection, `NeedsAck` state, `modules enable` prompt + ack persistence. `--lite` and `dense` layouts. Auto-fall to lite on narrow terminals.

**Phase 4 — polish (1 day)**
`vhs` tape + README, `THEMING.md`, `nord` theme, ASCII glyph fallback, goreleaser config for linux/amd64 + arm64 static builds, `completion`. Run `doctor` on pandalab (root and unprivileged) and inside a bare `debian:stable-slim` container and fix every `○`.

Don't start Phase N+1 with Phase N's tests red.

## 12. Open questions the implementer may decide alone

- Bubble Tea v1 vs v2 (pick whichever is stable; record it in `DECISIONS.md`).
- Exact table widget (Bubbles `table` vs hand-rolled) — hand-rolled is fine if Bubbles' can't do per-cell styling for the hotkey/glyph columns.
- Whether `logs` should be a tab or just a card on Overview in v1 — a card is acceptable if the tab isn't pulling its weight.
- Ordering of the curated port table and how many entries beyond 150.

Anything else that contradicts §1 or §10 is not the implementer's to decide — stop and ask.
