# PHALANX - How to Use

## Prerequisites

- Go 1.22+
- A browser (Chrome/Firefox/Edge)
- Enough RAM: ~1MB per 1000 nodes in steady state

---

## Running the simulation

```bash
cd sim
go run ./cmd/sim [N]
```

`N` = number of nodes to join at startup (default: 1000).

Examples:

```bash
go run ./cmd/sim            # 1 000 nodes
go run ./cmd/sim 50000      # 50 000 nodes
go run ./cmd/sim 1000000    # 1M nodes (needs ~10GB RAM, use the 120GB machine)
```

With runtime tuning flags:

```bash
go run ./cmd/sim -n 50000 -succ-min-q 0.74 -promo-min-ticks 30 -promo-cooldown-ticks 800 -overlay-refresh-ticks 100
```

The simulation will:
1. Bootstrap the root leader
2. Join N nodes using rank-driven placement
3. Start the tick loop (Q_i recomputed every 10ms)
4. Serve the WebUI at **http://localhost:8080**

Stop with `Ctrl+C`.

---

## WebUI

Open **http://localhost:8080** in a browser.

### Stats bar (top)

| Field | Meaning |
|-------|---------|
| Nodes | Total nodes in the mesh |
| Leaders | Nodes with active LeaderState |
| Max Level | Depth of the deepest node in the hierarchy |
| Subnets | Number of simulated broadcast domains |
| Avg Q | Mean final role score across all nodes |

### Topology canvas (center)

Radial tree of **leader nodes only** (leaves are aggregated into their parent count).

- **Center** = root leader (level 0)
- **Rings** = hierarchy levels (level 1, 2, ... outward)
- **Circle size** = proportional to cluster size (leader + successors + leaves)
- **Color** = Q_i score: green (high) -> yellow -> red (low)
- **Lines** = parent-child connections between leaders

At large scales (100k+ nodes) only leaders are shown, so the canvas remains readable.

### Controls (right panel)

**Add Nodes**
- Enter a count, click **Join**. Nodes are placed immediately via rank-driven placement.

**Fault Injection**
- **Kill random leader**: removes a random non-root leader and triggers failover for orphaned nodes.
- **Kill N leaders**: kills N leaders in sequence and records each failover.

**Failover Measurements**
- Each kill event records failover latency (microsecond resolution).
- Green = fast (< 1ms), yellow = slower.

---

## REST API

All endpoints accept and return JSON.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/stats` | Current Stats snapshot |
| GET | `/api/snapshot` | Full topology snapshot (leaders only) |
| POST | `/api/join?n=N` | Add N nodes (max 10 000 per call) |
| POST | `/api/kill` | Kill a random non-root leader |
| POST | `/api/kill?id=42` | Kill the specific node with ID 42 |

Example:

```bash
# Add 1000 nodes
curl -X POST "http://localhost:8080/api/join?n=1000"

# Kill a random leader and see failover latency
curl -X POST "http://localhost:8080/api/kill"

# Kill node #99
curl -X POST "http://localhost:8080/api/kill?id=99"

# Get full stats
curl http://localhost:8080/api/stats
```

---

## Configuration

Default values live in `sim/internal/mesh/mesh.go` -> `DefaultConfig()`.
At runtime you can override key values via CLI flags and/or env vars.

Precedence: `default < env < CLI`.

### CLI flags

| Flag | Meaning |
|------|---------|
| `-n` | Number of nodes to join at startup |
| `-succ-min-q` | Minimum Q required to be successor-eligible at join |
| `-promo-min-ticks` | Consecutive ticks above threshold before promotion |
| `-promo-cooldown-ticks` | Cooldown ticks after promotion to prevent churn |
| `-overlay-refresh-ticks` | Ticks between leader overlay refresh passes |

### Environment variables

| Env var | Meaning |
|---------|---------|
| `PHALANX_NODES` | Number of nodes to join at startup |
| `PHALANX_SUCCESSOR_MIN_Q` | Same as `-succ-min-q` |
| `PHALANX_PROMO_MIN_TICKS` | Same as `-promo-min-ticks` |
| `PHALANX_PROMO_COOLDOWN_TICKS` | Same as `-promo-cooldown-ticks` |
| `PHALANX_OVERLAY_REFRESH_TICKS` | Same as `-overlay-refresh-ticks` |

Example:

```bash
export PHALANX_SUCCESSOR_MIN_Q=0.75
export PHALANX_PROMO_MIN_TICKS=40
go run ./cmd/sim -n 50000
```

Backward compatibility: `go run ./cmd/sim 50000` (positional `N`) still works.

### Main defaults and effects

| Parameter | Default | Effect |
|-----------|---------|--------|
| `N` | 5 | Max successors per leader. Higher N = wider tree, more failover peers. |
| `Y` | 3 | Lower-level leaders tracked per node. Higher Y = more redundant vertical connections. |
| `TickPeriod` | 10ms | How often Q_i is recomputed for all nodes. Lower = more CPU, more responsive metrics. |
| `Hysteresis` | 0.07 | Min Q surplus to trigger a promotion. Higher = more stable leadership. |
| `SuccessorMinQ` | 0.72 | Join-time successor eligibility threshold (higher = fewer leaders). |
| `PromotionMinTicks` | 20 | Sustained superiority needed before promotion. |
| `PromotionCooldownTicks` | 500 | Post-promotion lockout to reduce leader ping-pong. |
| `OverlayRefreshTicks` | 200 | Refresh cadence for same-level/lower-level leader connectivity views. |
| `HRConfig.HRMin` | 0.30 | Min Historical Reliability to be eligible for any role. |
| `HRConfig.Rho` | 0.10 | Weight of HR in the final Q_i score. |
| `HRConfig.LambdaQ` | 0.10 | EMA smoothing speed for q_tilde_i. Lower = slower reaction. |

---

## Understanding the output

**Join phase log:**
```
join phase done: 2000 nodes in 30ms (65689 nodes/s)
initial state: leaders=92 base=1909 max_level=9 subnets=1 avgQ=0.6890
```

- `leaders`: number of nodes currently in leader role
- `max_level`: deepest leaf distance from root
- `avgQ`: starts near warm baseline and then stabilizes over ticks

**Steady-state log (every 2 seconds):**
```
nodes=2.0k   leaders=88   level=9   subnets=1
  avgQ=0.71  avgHR=0.71  Q_leaders=0.86  Q_base=0.71
  avgSucc=1.0  avgPeers=5.0  avgLower=3.0  promos_2s=0  promos_total=10
```

- `avgPeers` should converge near `N`
- `avgLower` should converge near `Y`
- `promos_2s` near zero indicates stable leadership

**Failover response:**
```json
{ "killed": 199, "failover_latency": "0s", "failover_us": 0, "remaining": 2000 }
```

- `failover_us=0` is in-process reconnection time (algorithmic baseline)
- real network transport adds RTT on top

---

## Tips for experiments

**Scalability measurement:**
```bash
for N in 1000 10000 100000 1000000; do
  go run ./cmd/sim $N &
  sleep 10
  curl -s http://localhost:8080/api/stats
  pkill -f "go run"
done
```

**Failover stress test:**
```bash
for i in $(seq 1 10); do
  curl -s -X POST http://localhost:8080/api/kill
done
```

**Watch hierarchy depth growth:**
Track `max_level` while adding nodes from the WebUI. It should approach `log_N(total_nodes)`.
