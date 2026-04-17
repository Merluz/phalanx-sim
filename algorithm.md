# PHALANX [nome carino] — Draft Metric Definitions

## Goal

Define a first formal draft for the node metrics used in the role-based quality function:

\[
q_i^{(r,c)}(t) = \sum_k w_k^{(r,c)} \cdot m_{i,k}(t)
\]

with:

- \(r \in \{leader, sender, base\}\)
- \(c \in \{LAN, WAN\}\)
- \(m_{i,k}(t) \in [0,1]\)

---

# 1. Heartbeat Regularity

## Symbol
\[
B_i(t)
\]

## Intuition
Measures how regularly the node emits and/or responds to heartbeat events.

A good node should:
- miss few heartbeats
- have low heartbeat jitter
- show regular inter-arrival timing

## Proposed definition

Let:

- \(miss_i(t)\) = normalized heartbeat miss ratio in a recent window
- \(jitter_i(t)\) = normalized heartbeat timing variance
- \(delay_i(t)\) = normalized heartbeat delay penalty

Then:

\[
B_i(t) = 1 - \left( \alpha_b \cdot miss_i(t) + \beta_b \cdot jitter_i(t) + \gamma_b \cdot delay_i(t) \right)
\]

with:

\[
\alpha_b + \beta_b + \gamma_b = 1
\]

and clipped into \([0,1]\).

## Notes
- useful especially in WAN
- partially overlaps with link quality, but remains focused on control-plane regularity
- should be computed over a short sliding window

---

# 2. Stability

## Symbol
\[
St_i(t)
\]

## Intuition
Measures whether the node behaves consistently over time.

A stable node should:
- avoid flapping
- avoid repeated reconnect/disconnect cycles
- avoid frequent reboots
- avoid frequent role changes

## Proposed definition

Let:

- \(f_i(t)\) = normalized flap score
- \(rb_i(t)\) = normalized reboot recency penalty
- \(rc_i(t)\) = normalized role-change penalty
- \(osc_i(t)\) = normalized state oscillation penalty

Then:

\[
St_i(t) = 1 - \left( \alpha_s f_i(t) + \beta_s rb_i(t) + \gamma_s rc_i(t) + \delta_s osc_i(t) \right)
\]

with:

\[
\alpha_s + \beta_s + \gamma_s + \delta_s = 1
\]

and clipped into \([0,1]\).

## Notes
- this is broader than heartbeat regularity
- this metric should strongly affect both leader and sender assignment
- reboot recency should decay over time, not remain constant forever

---

# 3. Link Quality

## Symbol
\[
L_i^{(r,c)}(t)
\]

## Intuition
Measures the quality of the relevant communication paths for the node, depending on role and context.

This must be role-relative.

- For a **leader**, it should reflect control-path quality toward coordination-relevant peers.
- For a **sender**, it should reflect forwarding/uplink path quality.

## Proposed definition

Let:

- \(loss_i^{(r,c)}(t)\) = normalized packet loss
- \(jit_i^{(r,c)}(t)\) = normalized jitter
- \(lat_i^{(r,c)}(t)\) = normalized latency
- \(burst_i^{(r,c)}(t)\) = normalized burst-loss penalty

Then:

\[
L_i^{(r,c)}(t) = 1 - \left(
\alpha_l loss_i^{(r,c)}(t) +
\beta_l jit_i^{(r,c)}(t) +
\gamma_l lat_i^{(r,c)}(t) +
\delta_l burst_i^{(r,c)}(t)
\right)
\]

with:

\[
\alpha_l + \beta_l + \gamma_l + \delta_l = 1
\]

and clipped into \([0,1]\).

## Notes
- in LAN, latency and jitter may matter less
- in WAN, this metric should become much more important
- for sender, this metric should likely carry one of the highest weights

---

# 4. System Health

## Symbol
\[
Y_i(t)
\]

## Intuition
Measures the local resource health of the node.

A healthy node should have:
- available CPU headroom
- free memory
- reasonable I/O load
- acceptable temperature / operational margin

## Proposed definition

Let:

- \(cpu_i(t)\) = normalized CPU pressure
- \(mem_i(t)\) = normalized memory pressure
- \(io_i(t)\) = normalized I/O pressure
- \(temp_i(t)\) = normalized thermal pressure

Then:

\[
Y_i(t) = 1 - \left(
\alpha_y cpu_i(t) +
\beta_y mem_i(t) +
\gamma_y io_i(t) +
\delta_y temp_i(t)
\right)
\]

with:

\[
\alpha_y + \beta_y + \gamma_y + \delta_y = 1
\]

and clipped into \([0,1]\).

## Notes
- this is especially important for leaders
- should saturate near critical conditions
- can be simplified if some signals are unavailable on certain devices

---

# 5. Reachability

## Symbol
\[
R_i(t)
\]

## Intuition
Measures how many relevant peers or segments the node can reach reliably.

A good node should:
- reach many peers
- keep those paths stable
- avoid being topologically isolated

## Proposed definition

Let:

- \(reach_i(t)\) = fraction of relevant reachable peers
- \(stabreach_i(t)\) = stability of those reachable paths

Then:

\[
R_i(t) = \alpha_r \cdot reach_i(t) + \beta_r \cdot stabreach_i(t)
\]

with:

\[
\alpha_r + \beta_r = 1
\]

## Notes
- in LAN, this may be very important for leader
- in WAN, reachability may need to be measured across clusters, not just local peers
- can be computed against all peers or against a role-relevant subset

---

# 6. Uplink Quality

## Symbol
\[
U_i(t)
\]

## Intuition
Measures the quality of the node’s external or inter-cluster connectivity.

This is especially important for senders and WAN scenarios.

## Proposed definition

Let:

- \(uploss_i(t)\) = normalized uplink loss
- \(uplat_i(t)\) = normalized uplink latency
- \(upjit_i(t)\) = normalized uplink jitter
- \(upavail_i(t)\) = normalized uplink availability

Then:

\[
U_i(t) =
\alpha_u \cdot (1-uploss_i(t)) +
\beta_u \cdot (1-uplat_i(t)) +
\gamma_u \cdot (1-upjit_i(t)) +
\delta_u \cdot upavail_i(t)
\]

with:

\[
\alpha_u + \beta_u + \gamma_u + \delta_u = 1
\]

## Notes
- low importance in pure LAN leadership
- high importance for sender in WAN
- may represent quality toward central server, gateway, or other clusters

---

# 7. Topology Convenience

## Symbol
\[
T_i(t)
\]

## Intuition
Measures how convenient the node is as a structural point in the graph.

A good topological node should:
- be close to many nodes
- reduce routing depth
- reduce reorganization cost when promoted

## Proposed definition

Let:

- \(deg_i(t)\) = normalized useful degree
- \(hop_i(t)\) = normalized average hop-distance penalty
- \(cov_i(t)\) = normalized coverage score

Then:

\[
T_i(t) =
\alpha_t \cdot deg_i(t) +
\beta_t \cdot cov_i(t) +
\gamma_t \cdot (1-hop_i(t))
\]

with:

\[
\alpha_t + \beta_t + \gamma_t = 1
\]

## Notes
- especially relevant for leaders
- in LAN this may be more relevant than in WAN
- should remain lightweight enough to compute online

---

# 8. Historical Reliability

## Symbol
\[
HR_i(t)
\]

## Intuition
Represents long-term accumulated trust in the node.

Unlike uptime, this metric captures whether the node has behaved well over time.

## Role in the system

$HR_i(t)$ lives outside the runtime quality function $q_i^{(r,c)}(t)$.  
It is updated using $q_i(t)$ only — never $Q_i(t)$ — to avoid circular dependency.  
It enters the system only in the final role score $Q_i(t)$.

## Final Role Score

\[
Q_i^{(r,c)}(t) =
\begin{cases}
(1-\rho)\,\tilde{q}_i^{(r,c)}(t) + \rho\,HR_i(t) & HR_i(t) \ge HR_{min} \\
0 & HR_i(t) < HR_{min}
\end{cases}
\]

with $\rho \in [0.05,\, 0.15]$ and $HR_{min} \in [0.2,\, 0.4]$

Where:
- $\tilde{q}_i^{(r,c)}$ = smoothed runtime quality (current snapshot)
- $HR_i$ = historical reliability (slow memory, updated from $q_i$ only)
- $\rho$ = weight of historical trust in final score
- $HR_{min}$ = minimum admissibility threshold

> A node with $HR_i < HR_{min}$ is excluded from ranking regardless of current quality.  
> Above threshold, $HR_i$ contributes linearly to $Q_i$.

## Update rule

\[
HR_i(t) =
\begin{cases}
(1-\alpha_{up})\,HR_i(t-\Delta t) + \alpha_{up}\,q_i(t) & q_i(t) \ge HR_i(t-\Delta t) \\
(1-\alpha_{down})\,HR_i(t-\Delta t) + \alpha_{down}\,q_i(t) & q_i(t) < HR_i(t-\Delta t)
\end{cases}
\]

with $\alpha_{down} > \alpha_{up}$

## Shock penalty

\[
HR_i(t^+) = HR_i(t^-) \cdot (1 - p_e), \quad p_e \in [0,1]
\]

## Notes
- trust builds slowly and decays faster
- very useful for leader and sender promotion
- can replace raw uptime as the main historical term

---

# 9. Summary

The role/context-aware quality function is built from normalized metrics:

\[
q_i^{(r,c)}(t) = \sum_k w_k^{(r,c)} \cdot m_{i,k}(t)
\]

where the candidate metrics are:

- \(B_i(t)\): Heartbeat Regularity
- \(St_i(t)\): Stability
- \(L_i^{(r,c)}(t)\): Link Quality
- \(Y_i(t)\): System Health
- \(R_i(t)\): Reachability
- \(U_i(t)\): Uplink Quality
- \(T_i(t)\): Topology Convenience

Historical trust is then tracked separately through:

- \(HR_i(t)\): Historical Reliability

---

# 10. Design notes

This is a first draft.

Some metrics may later:
- be merged
- be simplified
- receive asymmetric penalties
- be reweighted depending on role and context

The key idea is that node quality is:
- role-specific
- context-sensitive
- bounded
- incrementally computable
- suitable for both simulation and real deployment



### vol 2 ###
# PHALANX — Metric Definitions (Formal Draft)

##  Objective

Define a set of normalized, non-redundant metrics for role-based node evaluation in a distributed mesh system.

Each metric captures a distinct aspect of node behavior across:

- control-plane behavior
- data-plane performance
- structural topology
- resource availability
- long-term reliability

---

#  Core Quality Function

\[
q_i^{(r,c)}(t) = \sum_k w_k^{(r,c)} \cdot m_{i,k}(t)
\]

Where:

- \(r \in \{leader, sender, base\}\)
- \(c \in \{LAN, WAN\}\)
- \(m_{i,k}(t) \in [0,1]\)
- \(\sum_k w_k^{(r,c)} = 1\)

---

#  Weight Adaptation

\[
\tilde{w}_k^{(r,c)} = \bar{w}_k^{(r)} \cdot \gamma_k^{(c)}
\]

\[
w_k^{(r,c)} = \frac{\tilde{w}_k^{(r,c)}}{\sum_j \tilde{w}_j^{(r,c)}}
\]

---

#  Metrics Overview

| Symbol | Metric | Domain |
|--------|--------|--------|
| \(L_i\) | Link Quality | Data-plane (intra-domain) |
| \(St_i\) | Stability | Behavioral |
| \(Y_i\) | System Health | Local resources |
| \(R_i\) | Reachability | Runtime connectivity |
| \(T_i\) | Topology Convenience | Structural position |
| \(U_i\) | Uplink Quality | Inter-domain connectivity |
| \(HR_i\) | Historical Reliability | Long-term trust |

---

# 1. Link Quality \(L_i^{(r,c)}\)

## Definition

\[
L_i^{(r,c)}(t) = 1 - P_i^{(link)}(t)
\]

\[
P_i^{(link)} =
\alpha_l L_{loss} +
\beta_l L_{jit} +
\gamma_l L_{lat} +
\delta_l L_{burst}
\]

### Sub-metrics

\[
L_{loss} = 1 - e^{-loss / \tau_{loss}}
\]

\[
L_{jit} = \frac{jitter}{jitter + \theta_{jit}}
\]

\[
L_{lat} = \frac{latency}{latency + \theta_{lat}}
\]

\[
L_{burst} = 1 - e^{-burst\_size / \tau_{burst}}
\]

## Interpretation

Measures the quality of **role-relevant communication paths within the operational domain**.

- Leader → coordination peers
- Sender → forwarding paths

## Non-redundancy

- NOT equivalent to uplink quality (external vs internal)
- NOT equivalent to stability (transport vs behavior)

---

# 2. Stability \(St_i\)

## Definition

\[
St_i(t) = 1 - P_i^{(stability)}(t)
\]

\[
P_i^{(stability)} =
\alpha_s F_i +
\beta_s Rb_i +
\gamma_s Rc_i +
\delta_s Osc_i
\]

### Sub-metrics

\[
F_i = 1 - e^{-flap\_rate / \tau_{flap}}
\]

\[
Rb_i = e^{-t_{since\_reboot}/\tau_{rb}}
\]

\[
Rc_i = 1 - e^{-role\_changes / \tau_{rc}}
\]

\[
Osc_i = \frac{changes}{changes + \theta_{osc}}
\]

## Interpretation

Captures **behavioral consistency over time**.

## Non-redundancy

- NOT link quality → measures behavior, not network
- NOT reachability → measures cause, not effect

---

# 3. System Health \(Y_i\)

## Definition

\[
Y_i(t) = 1 - P_i^{(sys)}(t)
\]

\[
P_i^{(sys)} =
\alpha_y C_i +
\beta_y M_i +
\gamma_y I_i +
\delta_y Th_i +
\epsilon_y D_i
\]

### Sub-metrics

#### CPU pressure
\[
C_i = \frac{cpu_i^{p_c}}{cpu_i^{p_c} + \theta_c^{p_c}}
\]

#### Memory pressure
\[
M_i = \frac{mem\_used_i^{p_m}}{mem\_used_i^{p_m} + \theta_m^{p_m}}
\]

#### I/O pressure
\[
I_i = \frac{io_i^{p_{io}}}{io_i^{p_{io}} + \theta_{io}^{p_{io}}}
\]

#### Thermal pressure
\[
Th_i =
\begin{cases}
0 & temp \le T_{safe} \\
\frac{temp - T_{safe}}{T_{crit} - T_{safe}} & T_{safe} < temp < T_{crit} \\
1 & temp \ge T_{crit}
\end{cases}
\]

#### Degradation trend
\[
D_i = \text{clip}\!\left(\lambda_c\,\Delta cpu^+ + \lambda_m\,\Delta mem^+ + \lambda_{io}\,\Delta io^+,\; 0,\; 1\right)
\]

with $\lambda_c + \lambda_m + \lambda_{io} = 1$

## Interpretation

Measures **resource headroom and degradation trajectory**.

## Non-redundancy

- NOT link quality → internal vs network
- NOT stability → state vs resource capacity

---

# 4. Reachability \(R_i\)

## Definition

\[
R_i = \alpha_r R_{direct} + \beta_r R_{stable}
\]

\[
R_{direct} = \frac{N_{reachable}}{N_{expected}}
\]

\[
R_{stable} = 1 - e^{-t_{stable}/\tau_{reach}}
\]

## Interpretation

Measures **current operational connectivity**.

## Non-redundancy

- NOT topology → dynamic vs structural
- NOT link quality → global connectivity vs per-link quality

---

# 5. Topology Convenience \(T_i\)

## Definition

\[
T_i = \alpha_t Deg_i + \beta_t Cov_i + \gamma_t Prox_i
\]

\[
Deg_i = \frac{N_{neighbors}}{N_{max}}
\]

\[
Cov_i = \frac{N_{2-hop}}{N_{expected}}
\]

\[
Prox_i = \frac{1}{1 + avg\_hop}
\]

## Interpretation

Measures **structural usefulness within the graph**.

## Non-redundancy

- NOT reachability → structure vs runtime
- NOT link quality → position vs performance

---

# 6. Uplink Quality \(U_i\)

## Definition

\[
U_i(t) = 1 - P_i^{(uplink)}(t)
\]

\[
P_i^{(uplink)} =
\alpha_u U_{loss} +
\beta_u U_{jit} +
\gamma_u U_{lat} +
\delta_u U_{avail} +
\epsilon_u U_{path}
\]

### Sub-metrics

\[
U_{loss} = 1 - e^{-uploss/\tau_{uloss}}
\]

\[
U_{jit} = \frac{upjitter}{upjitter + \theta_{uj}}
\]

\[
U_{lat} = \frac{uplatency}{uplatency + \theta_{ul}}
\]

\[
U_{avail} = 1 - upavail_i(t)
\]

where $upavail_i(t) \in [0,1]$ is the normalized **unavailability** of the uplink

\[
U_{path} = 1 - e^{-path\_changes/\tau_{path}}
\]

## Interpretation

Measures **reliability of connectivity toward higher-level domains**.

## Non-redundancy

- NOT link quality → intra-domain vs inter-domain
- NOT reachability → connectivity vs exit reliability

---

# 7. Historical Reliability \(HR_i\)

## Role in the system

$HR_i$ is a slow memory updated from $q_i^{(r,c)}(t)$.  
It is **not** a term inside $q_i$ — it enters only in $Q_i$ at promotion time.

## Definition

\[
HR_i(t) =
\begin{cases}
(1-\alpha_{up})\,HR_i(t-\Delta t) + \alpha_{up}\,q_i(t) & q_i(t) \ge HR_i(t-\Delta t) \\
(1-\alpha_{down})\,HR_i(t-\Delta t) + \alpha_{down}\,q_i(t) & q_i(t) < HR_i(t-\Delta t)
\end{cases}
\]

with $\alpha_{down} > \alpha_{up}$

\[
HR_i(t^+) = HR_i(t^-) \cdot (1 - p_e)
\]

> **Note:** $HR_i$ is updated with $q_i(t)$ only — never with $Q_i(t)$.

## Interpretation

Models **long-term trust**.

## Non-redundancy

- NOT uptime → behavior vs duration
- NOT stability → aggregated history vs instant behavior

---

#  Key Design Principle

Each metric captures a **distinct layer of system behavior**:

| Layer | Metrics |
|------|--------|
| Data-plane | Link Quality |
| Behavioral | Stability |
| Resources | System Health |
| Runtime connectivity | Reachability |
| Structural graph | Topology |
| Inter-domain | Uplink |
| Temporal trust | Historical Reliability |

---

#  Final Insight

> Node quality is multi-dimensional, and no single metric is sufficient to capture suitability for role assignment in dynamic distributed systems.

### vol 3 ###

# 11. Normalization and Weighting Strategy

##  Objective

Define a consistent and stable approach for:

- metric normalization
- role-based weighting
- context adaptation (LAN/WAN)
- score stabilization

This section ensures that the model is:

- comparable across heterogeneous nodes
- stable over time
- adaptable to different deployment scenarios

---

# 11.1 Normalization Strategy

All raw metrics are mapped into normalized values:

\[
m_{i,k}(t) \in [0,1]
\]

using monotonic transformation functions.

## Function families

### Exponential saturation (for instability / errors)

\[
f(x) = 1 - e^{-x/\tau}
\]

Used for:
- packet loss
- burst loss
- flap rate

---

### Soft-threshold (Hill function)

\[
f(x) = \frac{x^p}{x^p + \theta^p}
\]

Used for:
- CPU pressure
- memory pressure
- I/O pressure

---

### Inverse decay (for latency-like metrics)

\[
f(x) = \frac{x}{x + \theta}
\quad \text{or} \quad
f(x) = \frac{1}{1 + x/\theta}
\]

Used for:
- latency
- jitter

---

## Context-dependent parameters

Normalization parameters are adapted to the network context:

\[
\theta = \theta^{(c)}, \quad \tau = \tau^{(c)}
\]

- LAN → stricter thresholds
- WAN → relaxed thresholds

---

# 11.2 Base Weights per Role

## Leader

\[
\bar{w}^{(leader)} =
\begin{cases}
St: 0.25 \\
Y:  0.20 \\
T:  0.20 \\
R:  0.15 \\
L:  0.10 \\
U:  0.10
\end{cases}
\]

### Rationale
- stability and system health dominate
- topology is critical for coordination
- HR excluded from $q_i$ — enters only via $Q_i$

---

## Sender

\[
\bar{w}^{(sender)} =
\begin{cases}
L:  0.25 \\
U:  0.25 \\
St: 0.15 \\
R:  0.15 \\
Y:  0.10 \\
T:  0.10
\end{cases}
\]

### Rationale
- communication quality dominates
- uplink reliability is critical
- HR excluded from $q_i$ — enters only via $Q_i$

---

## Base

\[
\bar{w}^{(base)} =
\begin{cases}
St: 0.30 \\
Y:  0.25 \\
R:  0.20 \\
L:  0.10 \\
T:  0.10 \\
U:  0.05
\end{cases}
\]

### Rationale
- base nodes are evaluated for **promotion readiness**, not operational assignment
- uplink carries minimal weight — base nodes have no inter-domain responsibility
- HR excluded from $q_i$ — tracked separately, used only at promotion time

---

# 11.3 Context Adaptation (LAN/WAN)

Weights are adjusted using context coefficients:

\[
\tilde{w}_k^{(r,c)} = \bar{w}_k^{(r)} \cdot \gamma_k^{(c)}
\]

\[
w_k^{(r,c)} = \frac{\tilde{w}_k^{(r,c)}}{\sum_j \tilde{w}_j^{(r,c)}}
\]

---

## LAN coefficients

\[
\gamma^{(LAN)} =
\begin{cases}
L: 0.8 \\
U: 0.5 \\
T: 1.2 \\
R: 1.2 \\
St: 1.0 \\
Y: 1.0
\end{cases}
\]

---

## WAN coefficients

\[
\gamma^{(WAN)} =
\begin{cases}
L: 1.3 \\
U: 1.5 \\
T: 0.8 \\
R: 1.0 \\
St: 1.2 \\
Y: 1.0
\end{cases}
\]

---

# 11.4 Score Smoothing

To prevent instability:

\[
\tilde{q}_i(t) = (1 - \lambda_q)\tilde{q}_i(t-\Delta t) + \lambda_q q_i(t)
\]

Where:

- \(\lambda_q \in (0,1)\)

### Suggested values

- leader: low \(\lambda_q\) (high stability)
- sender: medium \(\lambda_q\)

---

# 11.5 Soft Ranking

Ranking is computed locally within the **decision domain** $\mathcal{D}_r(i)$:

\[
score_i^{(r,c)} =
\frac{e^{\kappa\, Q_i^{(r,c)}}}
{\displaystyle\sum_{j \in \mathcal{D}_r(i)} e^{\kappa\, Q_j^{(r,c)}}}
\]

Where:
- $\mathcal{D}_r(i)$ = set of candidates visible to node $i$ for role $r$
  (e.g. cluster membership, leadership neighborhood)
- $Q_i^{(r,c)}$ = final role score (not $\tilde{q}_i$)
- $\kappa$ = sharpness parameter

> $Q_i^{(r,c)}$ is defined in Section — Final Role Score (Vol 2).
> The softmax is **not global** — it is scoped to the local decision domain.  
> The decision authority is the current cluster leader,  
> or the acting leader / top successor if the leader is unavailable.

### Benefits

- avoids abrupt role switching
- maintains competition between nodes
- improves stability

---

# 11.6 Hysteresis

Role transitions require a margin:

\[
\tilde{q}_i > \tilde{q}_{current} + \Delta
\]

Where:

- \(\Delta > 0\)

### Purpose

- prevents frequent role oscillation
- ensures meaningful promotion

---

# 11.7 Parameter Guidelines

| Parameter | Typical Range |
|----------|--------------|
| \(\lambda_q\) | 0.05 – 0.2 |
| \(\kappa\) | 5 – 10 |
| \(\Delta\) | 0.05 – 0.1 |

---

# 11.8 Bootstrap Tuning Parameters

Parameters are given per context as initial tuning ranges.  
They are not fixed — empirical refinement through simulation is expected.

## LAN

| Parameter | Range |
|-----------|-------|
| $\theta_{lat}^{LAN}$ | $3$–$5\,\text{ms}$ |
| $\theta_{jit}^{LAN}$ | $1$–$2\,\text{ms}$ |
| $\tau_{loss}^{LAN}$ | $0.01$–$0.02$ |
| $\tau_{burst}^{LAN}$ | $2$–$3\,\text{events/window}$ |
| $\theta_{ul}^{LAN}$ | $5$–$10\,\text{ms}$ |
| $\theta_{uj}^{LAN}$ | $1$–$3\,\text{ms}$ |
| $\tau_{path}^{LAN}$ | $2$–$4\,\text{changes/window}$ |
| $\tau_{flap}^{LAN}$ | $1$–$2\,\text{flaps/window}$ |
| $\tau_{rb}$ | $60$–$300\,\text{s}$ |
| $\tau_{rc}$ | $2$–$4\,\text{changes/window}$ |
| $\theta_{osc}$ | $2$–$5\,\text{osc/window}$ |
| $\tau_{reach}^{LAN}$ | $30$–$120\,\text{s}$ |
| $\theta_c$ | $0.70$–$0.80$ |
| $\theta_m$ | $0.60$–$0.75$ |
| $\theta_{io}$ | $0.70$–$0.85$ |
| $T_{safe}$ / $T_{crit}$ | $60^\circ\text{C}$ / $80^\circ\text{C}$ |

## WAN

| Parameter | Range |
|-----------|-------|
| $\theta_{lat}^{WAN}$ | $50$–$100\,\text{ms}$ |
| $\theta_{jit}^{WAN}$ | $10$–$20\,\text{ms}$ |
| $\tau_{loss}^{WAN}$ | $0.03$–$0.05$ |
| $\tau_{burst}^{WAN}$ | $3$–$5\,\text{events/window}$ |
| $\theta_{ul}^{WAN}$ | $80$–$150\,\text{ms}$ |
| $\theta_{uj}^{WAN}$ | $10$–$30\,\text{ms}$ |
| $\tau_{path}^{WAN}$ | $1$–$3\,\text{changes/window}$ |
| $\tau_{flap}^{WAN}$ | $1$–$2\,\text{flaps/window}$ |
| $\tau_{rb}$ | $120$–$600\,\text{s}$ |
| $\tau_{rc}$ | $2$–$4\,\text{changes/window}$ |
| $\theta_{osc}$ | $2$–$5\,\text{osc/window}$ |
| $\tau_{reach}^{WAN}$ | $60$–$300\,\text{s}$ |
| $\theta_c$ / $\theta_m$ / $\theta_{io}$ | same as LAN |
| $T_{safe}$ / $T_{crit}$ | $60^\circ\text{C}$ / $80^\circ\text{C}$ |

> **Note:** $\tau_{path}$ is stricter in WAN than LAN —  
> an unstable inter-domain uplink is more dangerous than an unstable intra-domain path.  
> System health parameters are context-independent: hardware is hardware.

---

# 11.9 Key Insight

> Stability emerges not from individual metrics, but from the interaction between normalization, weighting, and temporal smoothing.
