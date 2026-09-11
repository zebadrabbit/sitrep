# Claude Code kickoff prompt — sitrep, Phases 0 and 1

Paste this into Claude Code (Fable 5.1) from the repo directory on pandalab. It assumes `CLAUDE.md` and `docs/HANDOFF.md` are already in the repo.

---

Read `CLAUDE.md` and `docs/HANDOFF.md` in full before touching anything. Then plan and execute **Phase 0 and Phase 1** from HANDOFF §11. Do not start Phase 2.

Before writing code, tell me:

1. Which Bubble Tea major version you're using and why (check what's currently stable). Record it in `docs/DECISIONS.md`.
2. Your proposed `Module` interface as Go code, if it differs at all from HANDOFF §4.1, with the reason for each difference.
3. What you found on this host that affects the plan: `ss`/`systemctl`/`docker`/`smbstatus`/`exportfs` presence, whether I'm in the `docker` group, what listens right now (`ss -tulnp` unprivileged). Read-only discovery only — no installs, no state changes on this box.

Wait for my OK on those three, then build in this order, committing after each step with a message that says what's runnable:

**Phase 0**
- `go mod init github.com/zebadrabbit/sitrep`, cobra root, `version`, `config init|path|show`, `doctor` with only the Environment, Privileges and Config sections.
- Bubble Tea app that paints header (`sitrep ▸ hostname`), empty sidebar, footer, handles `q` and `?` (help overlay). Full and lite layouts; auto-fall to lite under 100×30.
- `internal/theme` with `amber` (default) and `mono`. No color literals anywhere outside this package.
- `Makefile` with `build`, `run`, `demo`, `test`, `screenshot`, `lint`, `install`.
- `--demo` and `--once` flags plumbed.

**Phase 1**
- `Module` interface, registry, per-module async collection loop with context timeouts, `DataMsg`, skeleton first paint. Measure cold start to first frame and print it in `doctor`.
- Overview tab composed from `Card`s.
- `system` module per HANDOFF §5 — keep it small, btop exists.
- `ports` module **complete** per HANDOFF §6: list view with AGE/CONN/IDENTITY, the four-layer identity resolver with confidence glyphs, the embedded curated port table (≥150 entries, overridable from config dir), `+` markers for new listeners, detail pane, and the opt-in `p` probe. The `docker▸` resolution is stubbed to "unknown container" until Phase 2 — leave a clear TODO.
- `sitrep ports [--json]` and `sitrep snapshot`.
- `doctor` gains the Modules and Ports sections.
- Fixtures under `testdata/fixtures/{system,ports}/` captured from this host with `SITREP_CAPTURE_FIXTURES=1` (redact anything private), plus synthetic ones covering: empty output, missing binary, malformed line, IPv6, UDP, a loopback-only listener, a docker-proxy owner, and a pid you can't read (needs-root case).
- Parser tests for everything. `make lint test screenshot` green.
- Run it for real: `./dist/sitrep` unprivileged and `sudo ./dist/sitrep`, and confirm Ports correctly identifies at least sshd, tailscaled, smbd, and the Docker containers on this box.

When Phase 1 is done, give me: the unprivileged and root `doctor` output, a screenshot of the Ports tab at 100×30, and a list of anything in HANDOFF you found ambiguous or disagreed with (with your reasoning — I want pushback, not silent workarounds).

Constraints you don't get to relax: read-only, no mouse, no settings UI in the TUI, no colors outside `theme`, no `exec.Command` outside `collect.Run`, no cgo.
