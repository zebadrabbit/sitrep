# Decisions

One line each, with the reason. HANDOFF.md is the contract; this file records what the implementer was allowed to decide (§12) and any deviation with its reason.

## 2026-09-10 — pre-flight

- **Bubble Tea v2** (`bubbletea/v2` v2.0.9, `lipgloss/v2` v2.0.6, `bubbles/v2` v2.2.1). v2 has been stable since early 2026 with nine patch releases; v1 (1.3.10, Sept 2025) is maintenance-only. Don't mix v1 and v2 packages.
- **Go toolchain 1.25 via `go.mod`.** The Charm v2 packages require Go ≥ 1.25. `/usr/local/go` on pandalab is 1.22.2; `GOTOOLCHAIN=auto` (the default) downloads the newer toolchain into `~/go/pkg/mod` on first build. No system install needed.
- **TOML: `github.com/BurntSushi/toml`.** Stdlib has no TOML. Smallest, best-known, no transitive deps.
- **gopsutil/v4** for CPU/mem/load/procs per HANDOFF §4.3. Cgo-free on Linux.

## v2 (ideas parked here, not built)

- Actions (`systemctl restart`, mounts) — v1 is read-only by contract.
- `sitrep serve` over SSH via Wish.
