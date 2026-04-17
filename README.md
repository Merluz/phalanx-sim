

**Reference simulator for the PHALANX distributed mesh protocol.**

A laboratory for validating topology, failover, and recovery behavior at scale —
from a handful of nodes to 10k+, all on a single machine.

---

## What this is

This repository is the **simulation** for [PHALANX](#about-phalanx) — a
self-organizing, zero-config distributed mesh protocol originally conceived in
the context of the Blackbird ecosystem and now developed as an independent
research artifact.

The simulator is where the protocol is exercised, measured, and tuned before
any of it touches real hardware:

- **Topology** — rank-driven placement, hierarchical cluster tree, bloom-filter subtree membership
- **Failover** — local and vertical failover cascades, frame-based retry, bootstrap symmetry
- **Rotation** — leader rotation policy with anti-flap, hysteresis, prepare/commit quorum
- **Recovery** — full epoch model (kill / restart / restart-random) with p50/p95/p99 latency distributions

Everything is observable in real time through a live web dashboard.

---

## Documentation

The specs and algorithms live alongside the code. Start here:

| Document                                                                    | Scope                                                                |
| --------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| [`overview.md`](./overview.md)                                              | Topology, connectivity, failover, bootstrap — the structural model   |
| [`algorithm.md`](./algorithm.md)                                            | Formal metric definitions, weighting strategy, normalization         |
| [`SPEC_MVP_CONTROL_PLANE.md`](./SPEC_MVP_CONTROL_PLANE.md)                  | Control plane baseline — discovery, session setup, placement, cache  |
| [`SPEC_LEADER_ROTATION_POLICY.md`](./SPEC_LEADER_ROTATION_POLICY.md)        | Leader rotation rules, anti-flap, quorum-based commit                |
| [`SPEC_RECOVERY_METRICS.md`](./SPEC_RECOVERY_METRICS.md)                    | Recovery epoch model and metric catalogue                            |
| [`RECOVERY_METRICS_CALCULATION.md`](./RECOVERY_METRICS_CALCULATION.md)      | How each recovery metric is actually computed                        |

---

## Running the simulator

Requirements: **Go 1.21+**.

### Simulation mode

```bash
cd sim
go run ./cmd/sim -h
go run ./cmd/sim -n 5000 -node-goroutines=true -async-spawn=true -spawn-parallelism=16
```

Each node can optionally run in its own goroutine, with sync or async spawn.

### Dashboard mode (recommended)

```bash
cd sim
go run ./cmd/dashboard -listen :8090 -web-dir ./web
# open http://localhost:8090
```

The dashboard is the fastest way to iterate on configuration — tweak a
parameter, see the mesh reorganize live.

### Convenience runners

```bash
# bash
./run.sh build
./run.sh dashboard-dev
./run.sh sim-smoke 300

# powershell
.\run.ps1 -Profile build
.\run.ps1 -Profile dashboard-dev -Port 8090
.\run.ps1 -Profile sim-load -Nodes 10000
```

---

## Dashboard

The web dashboard at `sim/web/` exposes the live mesh state, per-node quality
scores, topology graph, and ongoing recovery epochs. It talks to the
dashboard binary over a small HTTP/WS API.

> **Note on authorship:** the single `sim/web/index.html` file — the
> dashboard UI — was written by an AI assistant (Claude, Anthropic) on request,
> as a quick visualization layer over the Go backend. Backend, protocol, specs.

---

## Recovery testing

The simulator supports destructive operations with full epoch accounting:

- `kill` — remove a set of nodes
- `restart` — restart a set of nodes
- `restart_random` — random subset restart

Every destructive operation opens a **recovery epoch**. Metrics collected
per epoch include time-to-first-new-leader, time-to-50/90/99-connected,
topology stability window, parent/leader churn, and latency distributions
for reconnect / promotion / reparent events. See
[`SPEC_RECOVERY_METRICS.md`](./sim/SPEC_RECOVERY_METRICS.md) for the full
catalogue.

---

## About PHALANX

PHALANX is a distributed multi-agent, zero-config mesh protocol with:

- **O(1) connections per node** — independent of mesh size
- **O(1) failover** with pre-computed, rank-updated failover lists
- **Hierarchical topology** with local scope (no global state)
- **Role-based node quality function** $q_i^{(r,c)}(t)$ driving placement, promotion, and rotation
- **Recovery ≡ join** — isolated clusters rejoin as a unit, no special recovery path

---

## Status

**Pre-alpha, active research.** Expect frequent refactors, API changes,
and the occasional half-finished spec. The simulator is being driven by
what the protocol needs next — not by release milestones.

---

## License

*Apache 2.0*