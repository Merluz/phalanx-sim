# sim2 Leader Rotation Policy (Draft)

Status: Draft with MVP decision engine + prepare/commit ack quorum + basic choreography  
Scope: leader replacement within the same level

## 1. Objective

Define a simple and stable rule to replace a level leader when its quality
degrades relative to peers, while preventing oscillations.

## 2. Level Convention

Hierarchy convention is fixed:

1. root leader is level `0`
2. deeper/peripheral levels are `+1`

All calculations in this policy are done per-level only.

## 3. Definitions

Given one level `L`:

1. `q_leader`: current leader quality
2. `S_L`: non-leader nodes in level `L`
3. `mean_L`: average quality of `S_L` (leader excluded)
4. `best_L`: best candidate in `S_L` by:
   - `q` descending
   - `node_id` ascending on tie

## 4. Trigger Rule (Baseline)

Leader replacement is considered only if:

1. `q_leader < mean_L * (1 - alpha_drop)`

with default:

1. `alpha_drop = 0.20` (20%)

This is intentionally one-sided (leader underperforming), not symmetric.

## 5. Candidate Eligibility

A candidate can replace current leader only if:

1. candidate is `best_L`
2. `q_best >= q_leader + delta_min`

default:

1. `delta_min = 0.05`

## 6. Anti-Flap Rules

All must be satisfied before rotation:

1. condition holds for a stability window `T_window` (not a single sample)
2. current leader has served at least `T_min_tenure`
3. level is not in cooldown (`T_cooldown`) from previous rotation

default:

1. `T_window = 5s`
2. `T_min_tenure = 15s`
3. `T_cooldown = 15s`

## 7. Decision Procedure

## Phase A: Observe

1. periodically evaluate trigger condition for each level
2. store short rolling stats for `mean_L`, `q_leader`, `q_best`

## Phase B: Propose

1. if trigger + eligibility + anti-flap pass, create rotation proposal
2. proposal carries:
   - level
   - old leader
   - new leader
   - reason snapshot (`q_leader`, `mean_L`, `q_best`)

## Phase C: Commit (implemented MVP flow)

Current control frames:

1. `LEADER_ROTATE_PREPARE`
2. `LEADER_ROTATE_PREPARE_ACK`
3. `LEADER_ROTATE_COMMIT`
4. `LEADER_ROTATE_COMMIT_ACK`
5. `LEADER_ROTATE_APPLIED`

Current behavior:

1. manager sends `PREPARE` broadcast before apply
2. peers respond with `LEADER_ROTATE_PREPARE_ACK` (`accept` / `reject`)
3. manager commits only if prepare quorum is reached within timeout
4. manager applies same-level leader switch in-sim
5. manager broadcasts `COMMIT` and collects `LEADER_ROTATE_COMMIT_ACK`
6. manager emits `APPLIED` with topology revision metadata (commit quorum status logged)

Implementation note (current MVP):

1. decision engine and anti-flap checks are active
2. rotation applies as direct in-sim role switch + topology revision update
3. frame choreography is transport-visible and guarded by prepare+commit quorum (not full distributed consensus)

## 8. Topology Revision / Term

On successful rotation at level `L`:

1. increment `topology_revision`
2. update level leader identity
3. propagate revision in subsequent `HELLO_ACK` / topology updates

Nodes must reject stale updates (`revision` lower than known).

## 9. Cache/Failover Impact

After rotation, update in order:

1. same-level `N` candidate cache for level `L`
2. upper-level caches (`Y`) that reference level `L`
3. flattened failover lists derived from those caches

Propagation may be eventual, but revision must remain monotonic.

## 10. Logging Requirements

Emit structured logs for:

1. trigger detected
2. proposal created
3. proposal rejected (with reason)
4. commit applied
5. rollback (if commit fails in future transition phase)

Each event must include:

1. `trace_id`
2. `level`
3. `old_leader`
4. `new_leader` (if any)
5. `topology_revision`

## 11. Out of Scope (current draft)

1. multi-level chained rotations
2. concurrent split-brain resolution
3. weighted/median-based alternatives to mean
4. transition transport details (make-before-break frame choreography)

These will be added after real `N/Y` redistribution is in place.
