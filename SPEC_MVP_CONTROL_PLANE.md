# sim2 Control Plane Spec (MVP)

Status: Draft accepted for implementation  
Scope: `sim2` only (simulation behavior, not production networking)

## 1. Goals

Define a deterministic control-plane baseline for:

1. discovery via UDP
2. session setup via simulated WS
3. placement by `q_i`
4. local failover cache (`N`, `Y`)
5. safe future transitions (make-before-break)

No broad optimization in this phase. Priority is correctness + observability.

## 2. Frozen Decisions

1. IP assignment: sequential private IPv4 (`10.x.y.z`).
2. Sender threshold: `Q_sender_min = 0.7`.
3. Tie-break: if `q_i` is equal, lower `node_id` wins.
4. Failover same-level: leader keeps max `N` same-level candidates.
5. Local cache size rule:
   - same-level: `N`
   - upper levels: `Y` levels, each with `1 leader + N candidates`
   - total max known failover entries: `N + Y*(N+1)`
   - example `N=5, Y=3` => `23`.
6. Role updates are event-driven only for now (join/leave/failure), no periodic rebalance loop yet.
7. Level convention is fixed:
   - root leader at level `0`
   - each lower/peripheral layer increases by `+1`.

## 3. Node Local State

Each node manager must expose at least:

1. identity: `node_id`, `ip`, `q_i`, `level`
2. hierarchy position: `current_level`, `total_levels_known`, `topology_revision`
2. role: `base | sender | leader`
3. connection state: `solo | joining | connected | transitioning`
4. current leader info (if any)
5. active WS peers
6. same-level candidates (`max N`)
7. upper-level cache (`max Y`, each with `leader + N`)
8. flattened failover view (derived)

## 4. Control Packet Contract

Packets are carried in `WSFrame.Payload` as JSON.

### 4.1 Common metadata

`type`, `trace_id`, `epoch`, `sent_at`

`epoch` is mandatory for stale-update rejection in later phases.

### 4.2 HELLO

Mandatory payload:

1. origin identity (`id`, `ip`)
2. origin `q_i`

Useful extra payload:

1. origin role/level snapshot
2. sender threshold
3. known leader hint
4. capabilities

### 4.3 HELLO_RELAY

Mandatory payload:

1. original joining node identity and `q_i`
2. `via` node identity
3. direction (`up` or `down`)
4. hop metadata (`hop`, `max_hop`)

### 4.4 HELLO_ACK

Mandatory payload:

1. final leader identity
2. assigned role and level
3. accepted-by identity
4. `total_levels` and `topology_revision`

Useful extra payload:

1. same-level candidates (`N`)
2. upper-level cache (`Y`)
3. flattened failover list
4. status code (`accepted`, `retry`, `reject`)

## 5. Join Flow

## 5.1 Discovery phase

1. node in `solo` performs UDP broadcast (`DISCOVER`)
2. receives at least one UDP `OFFER`
3. stops broadcasting immediately when switching to `joining`

## 5.2 WS session phase

1. open WS simulated link to first accepted offer target
2. send `HELLO`
3. target either:
   - accepts locally and returns `HELLO_ACK`, or
   - forwards with `HELLO_RELAY`
4. origin applies final `HELLO_ACK` and becomes `connected`

## 5.3 Relay direction rule (MVP)

1. if candidate `q_i` is better than current handler's placement authority, relay `up`
2. if candidate belongs lower in hierarchy, relay `down`
3. stop when target level accepts and emits final ACK

## 6. Placement Rules (MVP baseline)

1. only one active leader per level
2. leader candidacy ordered by:
   - `q_i` descending
   - `node_id` ascending on tie
3. non-leader role:
   - `sender` if `q_i >= 0.7`
   - else `base`

## 7. Capacity Rules (`N`, `Y`)

## 7.1 Same-level capacity `N`

1. leader tracks top `N` same-level candidates
2. candidates are failover-ready for leader replacement
3. overflow nodes are marked as excess and forwarded per redistribution logic

## 7.2 Upper-level cache `Y`

1. each node stores at most `Y` upper levels
2. each upper level stores `leader + N` candidates
3. if fewer upper levels exist, cache remains shorter (no synthetic padding)

## 7.3 MVP Redistribution (implemented baseline)

Current MVP redistribution is deterministic and cascade-based:

1. join starts from handler level (node reached by WS)
2. upward check:
   - if joining node outranks current level leader, it is relayed toward root (`level-1`)
3. local insert:
   - node is inserted into target level candidate set
4. overflow cascade:
   - if level exceeds `leader + N`, lowest-ranked tail is pushed to `level+1`
   - repeated until all levels respect capacity
5. ranking order in every level:
   - `q_i` descending, `node_id` ascending on tie
6. role assignment per level:
   - top-ranked node => `leader`
   - others => `sender` if `q_i >= 0.7`, otherwise `base`

## 8. Transition Safety (next implementation phase)

For any reattachment or redistribution:

1. node enters `transitioning`
2. opens new WS path
3. validates ACK/health on new path
4. closes old path only after new path is stable
5. rollback to previous path on timeout/failure

This section is mandatory behavior target, even if partially stubbed initially.

## 9. Observability Requirements

Every critical event must be logged with `trace_id`:

1. UDP discover/offer
2. WS connect/open
3. HELLO send/recv
4. RELAY send/recv
5. ACK send/apply
6. role/leader change
7. transition enter/exit/rollback

UI must be able to show:

1. node role + connection state
2. leader link
3. failover count
4. relay activity (at least via event list)

## 10. Milestones

## M1: Protocol correctness

1. UDP -> WS -> HELLO/ACK path always completes or fails cleanly
2. no duplicate final ACK apply per trace
3. stale epoch updates are ignored

## M2: Hierarchy correctness

1. deterministic leader selection
2. same-level top `N` cache stable
3. upper cache bounded by `Y`

## M3: Redistribution correctness

1. overflow nodes redistributed toward periphery
2. no connection flap loops under moderate churn

## M4: Transition safety

1. make-before-break enforced
2. rollback works under forced target failure

## 11. Acceptance Scenarios

1. `2 nodes`: one leader elected deterministically, other role by threshold.
2. `N+1 nodes`: overflow appears and is handled without inconsistent state.
3. join through non-leader: relay path resolves to correct leader.
4. leader failure: replacement from same-level `N` list.
5. join storm (small scale): no stuck `joining` nodes.

## 12. Related Specs

1. Leader rotation baseline policy (same-level replacement):
   - `SPEC_LEADER_ROTATION_POLICY.md`
