# sim2 Recovery Metrics Calculation Guide

This document explains how each recovery metric is computed and how to
interpret it.

Reference spec: `sim2/SPEC_RECOVERY_METRICS.md`.

## 1) Notation and Data Sources

Core symbols:
- `t0`: epoch start (`started_at_ms`)
- `t`: sample timestamp
- `dt`: sample step in seconds
- `N0`: `nodes_total_at_t0`
- `L0`: `leaders_at_t0`

Sampling defaults:
- State sample interval: `200ms`
- Stable window: `10s`
- Epoch timeout: `5m`

Current runtime data sources:
- Node runtime state (`Role`, `ConnectionState`, `Manager` snapshot)
- Topology parent map (`parentByNode`), with fallback to `CurrentLeader`
- Packet counters (UDP/WS sent/recv totals)

## 2) Epoch Model Metrics

### `epoch_id`
- Calculation:
  - `ep-<YYYYMMDD-HHMMSS>-<sequence>`
- Meaning:
  - Unique identifier of one recovery run.
- Status:
  - Implemented.

### `trigger`
- Calculation:
  - Source operation (`kill|restart|restart_random`).
- Meaning:
  - Why the epoch was opened.
- Status:
  - Implemented.

### `started_at_ms`, `ended_at_ms`
- Calculation:
  - Start: wall-clock at epoch creation.
  - End: when status becomes `stabilized`, `timeout`, or `aborted`.
- Meaning:
  - Recovery window bounds.
- Status:
  - Implemented.

### `status`
- Calculation:
  - `running` while active.
  - `stabilized` when topology stable condition is met.
  - `timeout` when `t - t0 >= 5m`.
  - `aborted` when a new destructive action starts before completion.
- Meaning:
  - Final outcome of one epoch.
- Status:
  - Implemented.

### `nodes_total_at_t0`, `leaders_at_t0`
- Calculation:
  - Count running nodes and leaders at epoch open.
- Meaning:
  - Baseline scale for all ratio/time metrics.
- Status:
  - Implemented.

### `killed.total|leaders|senders|base`
- Calculation:
  - Taken from the mutation operation result that opens the epoch.
- Meaning:
  - Blast radius by role.
- Status:
  - Implemented.

## 3) Core Recovery Metrics

### `time_to_first_new_leader_ms`
- Calculation:
  - Track nodes that were not leader at `t0`.
  - First sample `t` where one of them becomes leader:
    - `time_to_first_new_leader_ms = t - t0`.
- Meaning:
  - How fast leadership regeneration starts.
- Status:
  - Implemented.

### `time_to_50_connected_ms`, `time_to_90_connected_ms`, `time_to_99_connected_ms`
- Calculation:
  - `connected_nodes(t)` = nodes with `ConnectionState == Connected`.
  - Threshold `T(p) = ceil(N0 * p)`, `p in {0.50, 0.90, 0.99}`.
  - First `t` with `connected_nodes(t) >= T(p)`:
    - `time_to_p_connected_ms = t - t0`.
- Meaning:
  - Reconnection speed at progressively stricter targets.
- Status:
  - Implemented.

### `time_to_topology_stable_ms`
- Calculation:
  - At each sample compare previous vs current:
    - `roleByNode`
    - `parentByNode`
  - If either changes, reset `lastTopologyChangeAt = t`.
  - Stable when no change for `10s`:
    - `time_to_topology_stable_ms = t - t0`.
- Meaning:
  - Time to stop role/parent churn.
- Status:
  - Implemented.

### `time_to_topology_stable_window_start_ms`
- Calculation:
  - Let `t_last_change` be the latest timestamp where role or parent changed.
  - When stability condition is reached, record:
    - `time_to_topology_stable_window_start_ms = t_last_change - t0`.
- Meaning:
  - When the final quiet `stable_window_ms` interval started.
  - In ideal conditions:
    - `time_to_topology_stable_window_start_ms + stable_window_ms ~= time_to_topology_stable_ms`.
- Status:
  - Implemented.

### `topology_stable_window_elapsed_ms`
- Calculation:
  - At each sample:
    - `topology_stable_window_elapsed_ms = max(0, t_now - t_last_change)`.
  - Reset to `0` immediately when role or parent changes.
- Meaning:
  - Real-time progress inside the current quiet window toward stability.
  - With stable window `10s`, values near `10000` mean stabilization is close.
- Status:
  - Implemented.

### `peak_disconnected_nodes`
- Calculation:
  - `disconnected(t) = total_running(t) - connected_nodes(t)`.
  - Track max over epoch.
- Meaning:
  - Worst transient isolation size.
- Status:
  - Implemented.

### `orphan_node_seconds`
- Calculation:
  - `orphan_nodes(t)`:
    - Leader orphan if not root and has no leader path and no active link.
    - Non-leader orphan if no leader path and no active link.
  - Discrete integral:
    - `orphan_node_seconds += orphan_nodes(t) * dt`.
- Meaning:
  - Aggregate orphan pressure over time.
- Status:
  - Implemented.

## 4) Latency Distribution Metrics

Families:
- `node_reconnect_ms`
- `leader_promotion_ms`
- `reparent_ms`

For each family:
- `count`, `sample_size` = number of completed samples in that family.
- Sort samples ascending.
- `min = s[0]`
- `p50 = s[ceil(n*0.50)-1]`
- `p95 = s[ceil(n*0.95)-1]`
- `p99 = s[ceil(n*0.99)-1]` only if `n >= 100`, otherwise `null`
- `max = s[n-1]`

Meaning:
- Distribution of how long each adaptation type takes.

Status:
- Implemented.

## 5) Control Plane Packet Counters

### UDP cumulative counters
- `packet_counters.udp.discover.{sent,recv}`
- `packet_counters.udp.offer.{sent,recv}`
- `packet_counters.udp.connect.{sent,recv}`

Calculation:
- Increment on successful send/enqueue path.
- `recv` reflects received datagrams/frames processed by handlers.

Meaning:
- Total control-plane volume by packet type.

Status:
- Implemented (`udp.connect.recv` currently fixed to `0` in API model).

### WS cumulative counters
- `hello`, `hello_ack`, `hello_relay`, `transition_hint`
- `leader_rotate_prepare`, `leader_rotate_prepare_ack`
- `leader_rotate_commit`, `leader_rotate_commit_ack`
- `leader_rotate_applied`

Calculation:
- `sent`: increment on successful WS send call.
- `recv`: increment when frame type is decoded and dispatched.

Meaning:
- Total WS control-plane traffic by semantic packet.

Status:
- Implemented.

## 6) Additional Metrics (Implemented + Planned)

### Churn Metrics

`leader_changes_total`
- Formula:
  - At each sample, count nodes where role transitioned `!= leader -> leader`.
- Meaning:
  - Leadership instability volume.
- Status:
  - Implemented.

`parent_changes_total`
- Formula:
  - At each sample, count nodes where `parentByNode_t != parentByNode_{t-1}`.
- Meaning:
  - Reparent churn volume.
- Status:
  - Implemented.

`role_flaps_total`
- Formula:
  - Count immediate reversals `A->B->A` on consecutive role transitions for the same node.
- Meaning:
  - Oscillation severity.
- Status:
  - Implemented.

`parent_changes_per_node_p95|p99`
- Formula:
  - For each node: total parent changes in epoch.
  - Report quantiles over node distribution.
- Meaning:
  - Tail churn concentration.

`time_in_transitioning_ms_p95|p99`
- Formula:
  - For each node integrate durations with `ConnectionState=Transitioning`.
  - Report quantiles across nodes.
- Meaning:
  - Tail penalty of reconfiguration.

### Bucketed Traffic Rate Metrics

`discover_rate_per_sec`, `offer_rate_per_sec`, `connect_rate_per_sec`,
`transition_hint_rate_per_sec`, `drops_inbox_full_per_sec`,
`drops_simulated_loss_per_sec`

- Formula:
  - Use `1s` buckets:
    - `rate[k] = counter(t_k) - counter(t_{k-1})`.
  - Report `avg`, `p95`, `p99`, `max` on rate series.
- Meaning:
  - Burstiness and sustained control-plane pressure.

### Topology Quality Metrics

`final_leaders_total`
- Formula:
  - Leaders count at epoch end.

`final_total_levels`
- Formula:
  - Max observed level + 1 at epoch end.

`followers_per_leader_avg|p95|p99|max`
- Formula:
  - Build follower count per leader at epoch end; report stats.

`nodes_with_valid_path_to_root_pct`
- Formula:
  - `% nodes` with acyclic parent path that reaches root within hop bound.

`invariant_violations_total`
- Formula:
  - Sum violated invariants during epoch:
    - missing parent for non-root attached node,
    - cycle in parent chain,
    - impossible level relation, etc.

### Performance Metrics

`tick_duration_ms_p95|p99|max`
- Formula:
  - Sample per-tick processing time; report quantiles.

`tick_overrun_total`
- Formula:
  - Count ticks where `tick_duration > tick_period`.

`goroutines_p95|p99`
- Formula:
  - Sample `runtime.NumGoroutine()` periodically; report quantiles.

`heap_alloc_mb_p95|p99`
- Formula:
  - Sample `runtime.MemStats.HeapAlloc`; report quantiles in MB.

`gc_pause_ms_p95|p99`
- Formula:
  - Sample GC pause durations from runtime stats; report quantiles.

`api_topology_payload_kb_p95|p99`
- Formula:
  - Sample encoded `/api/topology` payload size; report quantiles in KB.

## 7) Practical Interpretation Guide

- Fast `time_to_first_new_leader_ms` but slow `time_to_90_connected_ms`:
  - leadership recovers quickly, follower reconnection is bottleneck.
- Low `peak_disconnected_nodes` but high `orphan_node_seconds`:
  - few nodes impacted, but for too long.
- High `reparent_ms` tail with high `parent_changes_total`:
  - topology oscillation likely; add hysteresis/cooldown.
- High `transition_hint` with high `connect_refresh` (future split):
  - signaling duplication, candidate for dedup/cooldown optimization.

## 8) Implementation Mapping

Current implementation files:
- Recovery epoch engine:
  - `sim2/internal/dashboard/recovery.go`
- General metrics and counters:
  - `sim2/internal/dashboard/metrics.go`
- Packet emission paths:
  - `sim2/internal/dashboard/manager.go`

This guide should be updated whenever formulas or sampling semantics change.
