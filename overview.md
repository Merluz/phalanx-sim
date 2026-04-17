Topology, Connectivity and Failover

## Objective

Define the structural and operational model of the PHALANX mesh:

- node hierarchy and rank-driven placement
- connection model and message filtering
- failover cascade and vertical propagation
- loop-breaking mechanism
- bootstrap and recovery symmetry

---

# 1. Core Design Principle

> All structural properties of the mesh emerge from a single mechanism:
> the dynamic role score $Q_i^{(r,c)}(t)$.

No manual configuration is required.
Topology, failover lists, and promotions are fully self-organizing.

---

# 2. Hierarchy Model

## 2.1 Levels and Roles

The mesh is organized as a **hierarchical cluster tree**.

Each level $k$ contains:

- one **Leader** node $\ell_k$
- $N$ **Successor nodes** $s_k^{(1)}, \ldots, s_k^{(N)}$
- $N^2$ **Leaf nodes** (attached to successors)

Successor nodes are same-level peers with ranking high enough
to be included in the leader's successor set.
They are ordinary nodes — not manually designated.

## 2.2 Structural Parameters

| Parameter | Description |
|-----------|-------------|
| $N$ | Number of successors per leader (TBD via simulation) |
| $Y$ | Number of lower-level leaders known per node |
| $k$ | Hierarchy depth level |

## 2.3 Example

For $N = 5$, $Y = 3$, a single level contains:

$$
1 \text{ leader} + 5 \text{ successors} + 25 \text{ leaves} = 31 \text{ nodes}
$$

Each leader node maintains:

- $N$ WebSocket connections → same-level successors
- $1$ WebSocket connection → upper-level leader
- $Y$ WebSocket connections → lower-level leaders
- occasional random connections → discovery

**Total connections per leader node: $N + Y + 1 + \epsilon$**

This is $O(1)$ per node, independent of mesh size.
This is the primary reason PHALANX scales to $10^6$+ nodes.

---

# 3. Node Join and Rank-Driven Placement

## 3.1 Join Procedure

When a new node $i$ joins the mesh:

1. connects to a **random known node** $j$
2. presents its current score $Q_i$
3. $j$ forwards $i$ toward a more appropriate node,
   propagating upward as long as $Q_i > Q_{handler}$
4. placement terminates when $Q_i \le Q_{handler}$

## 3.2 Placement Outcome

$$
\text{place}(i) =
\begin{cases}
\text{successor / leader level } k & Q_i \ge \theta_k \\
\text{leaf under successor} & Q_i < \theta_{min}
\end{cases}
$$

## 3.3 Knowledge Transfer at Join

Upon placement, the accepting leader $\ell_k$ **pushes** to node $i$:

- current successor list $\{s_k^{(1)}, \ldots, s_k^{(N)}\}$
- known upper leaders (up to $N_{hop}$ levels)
- known lower leaders (up to $Y$ levels)
- failover priority list

This is a one-time push at connection time.
No explicit request from $i$ is needed.

## 3.4 Successor List Update

When a new high-ranking node joins at level $k$,
the leader $\ell_k$ notifies:

- all current successors $s_k^{(j)}$
- upper leader $\ell_{k+1}$
- lower leaders $\ell_{k-1}^{(1\ldots Y)}$

Each notified node updates its local failover list accordingly.

---

# 4. Message Filtering

## 4.1 Locality Principle

> A node only processes messages relevant to its level and role.
> It does not observe global mesh state.

## 4.2 Filtering Model

Messages are filtered by level at each hop:

$$
\text{forward}(m, i) =
\begin{cases}
\text{process locally} & \text{level}(m) = \text{level}(i) \\
\text{forward up} & \text{level}(m) > \text{level}(i) \\
\text{drop} & \text{level}(m) < \text{level}(i) - N_{hop}
\end{cases}
$$

## 4.3 Election Message Cost

A local election at level $k$ involves only $\mathcal{D}_r(i)$:

$$
|\mathcal{D}_r(i)| = N + 1
$$

Election message cost: $O(N)$ — independent of global mesh size.

Information propagates upward only on:
- leader failure
- significant rank change
- topology reorganization request

---

# 5. Rank-Driven Promotion and Demotion

## 5.1 Promotion Condition

A successor $s_k^{(j)}$ replaces leader $\ell_k$ when:

$$
Q_{s}(t) > Q_{\ell}(t) + \Delta
$$

where $\Delta$ is the hysteresis margin defined in the weighting model.

The current leader **delegates** its role upon detecting this condition.
Promotion is not externally triggered — it is self-initiated by the leader.

## 5.2 Demotion

A demoted leader becomes a successor node at its level,
or descends to a lower level if $Q_i$ no longer meets the threshold.

## 5.3 Failover List Dynamics

The successor list is updated:

- at every high-ranking node join
- at every promotion / demotion event
- periodically via $Q_i$ recomputation

The failover list is therefore **always consistent with current ranking**.

---

# 6. Failover Model

## 6.1 Failover List Structure

Each node $i$ maintains an ordered failover list:

$$
F_i = [f_i^{(1)},\, f_i^{(2)},\, \ldots,\, f_i^{(M)}]
$$

ordered by $Q_j$ descending at the time of last update.

## 6.2 Local Failover (same level)

Upon leader failure, all nodes in the cluster autonomously
connect to $f_i^{(1)}$ (first available successor).

No election is needed: the successor list is already known.
Failover latency: **order of milliseconds**.

## 6.3 Vertical Failover Propagation

If a mid-level leader $\ell_k$ fails:

1. its children (leaders at level $k-1$) detect the failure independently
2. each child connects to its own failover list entry for level $k$
3. the new level-$k$ leader (first available successor) notifies $\ell_{k+1}$
4. $\ell_{k+1}$ updates its lower-leader list

Propagation is **bottom-up and asynchronous**.
Upper levels are notified only after local recovery is complete.

## 6.4 Cascade Failover

If multiple levels fail simultaneously:

- each level recovers independently
- upper levels see the recovery notification, not the intermediate failures
- overhead at upper levels: $O(Y)$ reconnections + $O(1)$ notification messages

---

# 7. Loop-Breaking Mechanism

## 7.1 Problem Statement

If all known failover targets are simultaneously unreachable,
a node risks cycling indefinitely through a stale failover list.

## 7.2 Frame-Based Retry Model

Time is divided into discrete **retry frames** of duration $T_{frame}$.

Within each frame window of $W$ frames:

$$
\text{retry slot}: \quad \text{every } \delta \text{ frames, attempt next } f_i^{(j)}
$$

$$
\text{discovery slot}: \quad \text{every } W \text{ frames, attempt random node}
$$

Visually:
```
Frame:   0      δ      2δ     3δ     ...    W
         |      |      |      |             |
         f(1)   f(2)   f(3)   f(4)   ...   [random]
```

## 7.3 Delay Calculation

The inter-slot delay $\delta$ is computed as:

$$
\delta_i^{(j)} = \delta_{base} \cdot \phi(Q_{prev}^{(j)}) + \epsilon
$$

where:

- $Q_{prev}^{(j)}$ = last known score of target cluster $j$
- $\phi(\cdot)$ = inverse quality penalty:
  $\phi(x) = 1 + (1-x) \cdot \alpha_\delta$
- $\epsilon \sim \mathcal{U}(0, \epsilon_{max})$ = uniform random jitter

**Interpretation:** clusters with lower previous quality are retried
with longer delay. Jitter prevents synchronized retry storms.

## 7.4 Adaptive Window

The frame window duration $T_{frame}$ is defined as:

$$
T_{frame} = \beta \cdot \tau_{rb} + \gamma \cdot T_{hb}
$$

where:

- $\tau_{rb}$ = reboot recovery timescale (from stability metrics)
- $T_{hb}$ = heartbeat interval
- $\beta, \gamma$ = tuning coefficients (TBD via simulation)

**Rationale:** the window should be long enough to allow
a recently rebooted cluster to recover, but short enough
to not leave the node isolated unnecessarily.

## 7.5 Bootstrap Trigger

If no successful connection is established within the window $W$:

$$
\text{all } f_i^{(j)} \text{ failed} \wedge \text{random attempt failed}
\implies \text{trigger full bootstrap}
$$

The node treats itself as a **new node** and restarts the join procedure.

---

# 8. Bootstrap and Recovery Symmetry

## 8.1 Core Principle

> Recovery is not a special case.
> An isolated cluster is treated as a new node joining the mesh.

## 8.2 Isolated Cluster Recovery

When a cluster partition is detected:

1. the isolated cluster attempts failover normally (Section 6)
2. if failover exhausted → frame-based retry (Section 7)
3. if window expires → bootstrap triggered

At bootstrap, the cluster:
- preserves its **internal structure** (existing connections, successor list)
- reconnects as a unit to a random reachable node
- undergoes rank-driven placement as a group

**Benefit:** internal nodes do not need to rediscover each other.
Only the cluster's external connectivity is rebuilt.

## 8.3 Symmetry Property

$$
\text{join}(new\_node) \equiv \text{recovery}(isolated\_cluster)
$$

This eliminates a separate recovery code path,
reducing implementation complexity and failure surface.

---

# 9. Connectivity Summary

| Connection Type | Count per node | Purpose |
|----------------|---------------|---------|
| Same-level successors | $N$ | failover, election domain |
| Upper leader | $1$ | vertical propagation |
| Lower leaders | $Y$ | topology awareness |
| Random | $\epsilon$ (occasional) | discovery, loop-breaking |

**Total: $N + Y + 1 + \epsilon$ — constant per node.**

---

# 10. Design Properties

| Property | Mechanism |
|----------|-----------|
| Zero-config | rank-driven placement |
| $O(1)$ connections per node | bounded $N$, $Y$ |
| $O(N)$ election cost | local decision domain |
| Loop-free failover | frame window + bootstrap trigger |
| Recovery = join | bootstrap symmetry |
| Minimal RAM per node | local knowledge only, $N_{hop}$ bounded |


# 11. Direct Node Communication

## 11.1 Problem Statement

The mesh is a **control and coordination plane** — not a data transport layer.
However, applicative sidecars may require direct node-to-node communication.

Since each node holds only local knowledge,
global IP lookup tables are not feasible:

- full ARP-like tables → too much RAM
- IP-based organization → fragile under topology changes

The solution is a **transparent bridge** mechanism inspired by
layer-2 bridge forwarding, combined with LCA traversal on the cluster tree.

---

## 11.2 Mechanism Overview

Direct communication between node $A$ and node $B$ proceeds in two phases:

**Phase 1 — Discovery** $O(k)$

The request traverses the hierarchy upward until the
**Lowest Common Ancestor** $\ell_{LCA}(A,B)$ is found,
then descends toward $B$:

\[
A \;\to\; \ell_A \;\to\; \ell_{A+1} \;\to\; \cdots \;\to\;
\ell_{LCA} \;\to\; \cdots \;\to\; \ell_B \;\to\; B
\]

Each leader performs a **local lookup only**:

\[
\text{route}(B, \ell_k) =
\begin{cases}
\text{forward down toward } B & B \in \mathcal{S}(\ell_k) \\
\text{forward up} & B \notin \mathcal{S}(\ell_k)
\end{cases}
\]

where $\mathcal{S}(\ell_k)$ is the subtree summary of leader $\ell_k$.

**Phase 2 — Direct connection**

Once $B$ is located, a **temporary direct connection** $A \leftrightarrow B$
is established outside the mesh.

The connection is:
- managed entirely by the applicative layer
- closed by the applicative when no longer needed
- invisible to the mesh control plane

---

## 11.3 Subtree Membership — Bloom Filter

To enable $O(k)$ traversal without global state,
each leader $\ell_k$ maintains a **bloom filter** $\mathcal{B}_k$
summarizing the IP addresses of all nodes in its subtree.

\[
\mathcal{B}_k = \text{BloomFilter}\!\left(\bigcup_{j \le k} \text{IPs}(\mathcal{S}(\ell_j))\right)
\]

### Lookup semantics

\[
B \in \mathcal{S}(\ell_k) \iff \mathcal{B}_k.\text{query}(IP_B) = \text{true}
\]

Bloom filters guarantee:

- **zero false negatives** — a node in the subtree is always found
- **false positive rate** $\epsilon_{bf}$ — controllable via filter size

A false positive causes the traversal to descend one extra level
before correcting upward — a bounded, acceptable overhead.

### Memory cost

For $N = 5$ successors, each leader manages at most $N^2 = 25$ leaf IPs
plus $Y$ lower-level subtrees.

With $\epsilon_{bf} = 0.01$, a bloom filter for 25 elements
requires approximately:

\[
m \approx -\frac{n \ln \epsilon_{bf}}{(\ln 2)^2} \approx 240 \text{ bits} \approx 30 \text{ bytes}
\]

Each leader holds one bloom filter per level below it.
With $k_{max} \approx 9$ levels (for $10^6$ nodes),
total bloom filter RAM per leader:

\[
RAM_{bf} \approx 9 \times 30 \approx 270 \text{ bytes}
\]

**Negligible.**

---

## 11.4 Traversal Complexity

| Scenario | Complexity |
|----------|-----------|
| $A$ and $B$ in same cluster | $O(1)$ |
| $A$ and $B$ under same level-$k$ leader | $O(k)$ |
| Worst case — root LCA | $O(2k_{max})$ |

For $10^6$ nodes with branching factor $N = 5$:

\[
k_{max} = \log_N(10^6) \approx 9 \implies \text{worst case} \approx 18 \text{ hops}
\]

---

## 11.5 Design Properties

| Property | Value |
|----------|-------|
| Global IP tables | not required |
| RAM per leader | $\approx 270$ bytes (bloom filters) |
| Traversal cost | $O(2k)$ |
| Mesh overhead | zero — direct conn is outside mesh |
| False negative rate | $0$ |
| False positive rate | $\epsilon_{bf}$ (tunable) |

---

## 11.6 Bloom Filter Update

The bloom filter $\mathcal{B}_k$ is updated on:

- node join → insert $IP_i$
- node leave / failure → **rebuild** $\mathcal{B}_k$ from current membership

> Bloom filters do not support deletion natively.
> On node leave, the filter is rebuilt from the current
> leader membership list — a $O(n_{cluster})$ operation,
> where $n_{cluster} \le N^2 + N$ is small by construction.

---

## 11.7 Usage Scope

Direct node communication is:

- **rare** — triggered only by applicative sidecar requests
- **ephemeral** — connection lifetime managed by applicative
- **decoupled** — does not affect mesh topology or failover state

It is not a general-purpose routing mechanism.
It is a best-effort bridge for applicative use cases
that require point-to-point communication outside normal
mesh message flow.