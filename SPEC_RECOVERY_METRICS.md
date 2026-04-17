# sim2 Recovery Metrics Spec

This spec defines recovery metrics for large-scale fault scenarios (for example:
`10k nodes + kill all leaders`) with emphasis on `p95/p99`.

## 1) Recovery Epoch Model

Every destructive operation opens a recovery epoch:
- `kill`
- `restart`
- `restart_random`

Epoch fields:
- `epoch_id` (string)
- `trigger` (`kill|restart|restart_random`)
- `started_at_ms` (`t0`)
- `ended_at_ms` (set on `stabilized` or `timeout`)
- `status` (`running|stabilized|timeout|aborted`)
- `nodes_total_at_t0`
- `leaders_at_t0`
- `killed_total`
- `killed_leaders`
- `killed_senders`
- `killed_base`

## 2) API Endpoints

Keep `GET /api/metrics` lightweight. Add:

- `GET /api/metrics/recovery/current`
- `GET /api/metrics/recovery/epochs?limit=20`
- `GET /api/metrics/recovery/epochs/{epoch_id}`

## 3) Core Recovery Metrics

All timings are measured from `t0`.

1. `time_to_first_new_leader_ms`
- First time a node becomes leader and was not leader at `t0`.

2. `time_to_50_connected_ms`
3. `time_to_90_connected_ms`
4. `time_to_99_connected_ms`
- First `t` where `connected_nodes(t) / nodes_total_at_t0 >= threshold`.

5. `time_to_topology_stable_ms`
- First `t` where no leader change and no parent change happen for
  `stable_window_ms` (default `10000`).

6. `time_to_topology_stable_window_start_ms`
- Start timestamp (relative to `t0`) of the final `stable_window_ms`
  interval that leads to `time_to_topology_stable_ms`.

7. `topology_stable_window_elapsed_ms`
- Running gauge: `now - last_topology_change_at` (resets to `0` on any leader
  or parent change).
- Useful to see in real time how close the epoch is to stable topology.

8. `peak_disconnected_nodes`
- Maximum disconnected nodes during epoch.

9. `orphan_node_seconds`
- Discrete integral: `sum(orphan_nodes(t) * sample_interval_s)`.

## 4) Latency Distributions (Quantiles)

For each metric family report:
- `count`, `min`, `p50`, `p95`, `p99`, `max`, `sample_size`

Families:
- `node_reconnect_latency_ms`
- `leader_promotion_latency_ms`
- `reparent_latency_ms`

Rule:
- If `sample_size < 100`, return `p99 = null`.

## 5) Churn Metrics

- `leader_changes_total`
- `parent_changes_total`
- `role_flaps_total`
- `parent_changes_per_node_p95`
- `parent_changes_per_node_p99`
- `time_in_transitioning_ms_p95`
- `time_in_transitioning_ms_p99`

## 6) Control Plane Traffic Metrics

Use 1-second buckets:
- `discover_rate_per_sec`
- `offer_rate_per_sec`
- `connect_rate_per_sec`
- `transition_hint_rate_per_sec`
- `drops_inbox_full_per_sec`
- `drops_simulated_loss_per_sec`

For each bucketed series report:
- `avg`, `p95`, `p99`, `max`

Additionally expose cumulative packet counters by packet type:
- `packet_counters.udp.discover.{sent,recv}`
- `packet_counters.udp.offer.{sent,recv}`
- `packet_counters.udp.connect.{sent,recv}`
- `packet_counters.ws.hello.{sent,recv}`
- `packet_counters.ws.hello_ack.{sent,recv}`
- `packet_counters.ws.hello_relay.{sent,recv}`
- `packet_counters.ws.transition_hint.{sent,recv}`
- `packet_counters.ws.leader_rotate_prepare.{sent,recv}`
- `packet_counters.ws.leader_rotate_prepare_ack.{sent,recv}`
- `packet_counters.ws.leader_rotate_commit.{sent,recv}`
- `packet_counters.ws.leader_rotate_commit_ack.{sent,recv}`
- `packet_counters.ws.leader_rotate_applied.{sent,recv}`

## 7) Topology Quality Metrics

- `final_leaders_total`
- `final_total_levels`
- `followers_per_leader_avg`
- `followers_per_leader_p95`
- `followers_per_leader_p99`
- `followers_per_leader_max`
- `nodes_with_valid_path_to_root_pct`
- `invariant_violations_total`

## 8) Performance Metrics

- `tick_duration_ms_p95`
- `tick_duration_ms_p99`
- `tick_duration_ms_max`
- `tick_overrun_total`
- `goroutines_p95`
- `goroutines_p99`
- `heap_alloc_mb_p95`
- `heap_alloc_mb_p99`
- `gc_pause_ms_p95`
- `gc_pause_ms_p99`
- `api_topology_payload_kb_p95`
- `api_topology_payload_kb_p99`

## 9) Sampling / Runtime Defaults

Recommended defaults for large scenarios:
- State sampling interval: `200ms`
- Traffic bucket width: `1s`
- Stable window: `10000ms`
- Epoch timeout: `300000ms` (5 minutes)

## 10) JSON Shape Example

```json
{
  "epoch_id": "ep-20260331-0012",
  "trigger": "kill",
  "started_at_ms": 1774912345000,
  "status": "running",
  "killed": {
    "total": 800,
    "leaders": 800,
    "senders": 0,
    "base": 0
  },
  "summary": {
    "nodes_total_at_t0": 10000,
    "leaders_at_t0": 3200,
    "time_to_first_new_leader_ms": 420,
    "time_to_50_connected_ms": 1800,
    "time_to_90_connected_ms": 5200,
    "time_to_99_connected_ms": 11100,
    "topology_stable_window_elapsed_ms": 10000,
    "time_to_topology_stable_window_start_ms": 8900,
    "time_to_topology_stable_ms": 18900,
    "peak_disconnected_nodes": 7810,
    "orphan_node_seconds": 9321.5
  },
  "latency": {
    "node_reconnect_ms": {
      "count": 9980,
      "p50": 2100,
      "p95": 7400,
      "p99": 12900,
      "max": 20100
    },
    "leader_promotion_ms": {
      "count": 790,
      "p50": 900,
      "p95": 2800,
      "p99": 4300,
      "max": 6100
    },
    "reparent_ms": {
      "count": 9960,
      "p50": 1700,
      "p95": 6200,
      "p99": 11800,
      "max": 19000
    }
  },
  "traffic_rate": {
    "discover_per_sec": { "p95": 8200, "p99": 12000 },
    "offer_per_sec": { "p95": 7900, "p99": 11800 },
    "connect_per_sec": { "p95": 2100, "p99": 3200 }
  },
  "churn": {
    "leader_changes_total": 1420,
    "parent_changes_total": 28100,
    "role_flaps_total": 6100
  }
}
```

## 11) Acceptance Criteria

1. Recovery epoch is opened/closed deterministically for each destructive action.
2. `p95/p99` are computed from explicit sample buffers with known size.
3. `time_to_90_connected_ms` and `time_to_topology_stable_ms` are always reported
   (or marked as timeout).
4. Metrics endpoint remains lightweight enough for 1s polling.

## 12) MVP API Contract (Implemented)

The dashboard exposes:

- `GET /api/metrics/recovery/current`
- `GET /api/metrics/recovery/epochs?limit=20`
- `GET /api/metrics/recovery/epochs/{epoch_id}`

Behavior notes:

- Each new destructive action opens a fresh epoch and aborts an in-flight one.
- Final statuses are: `stabilized`, `timeout`, `aborted`.
- `p99` is emitted only when `sample_size >= 100`, otherwise `null`.
- Sampling interval is `200ms`, stability window is `10s`, timeout is `5m`.
