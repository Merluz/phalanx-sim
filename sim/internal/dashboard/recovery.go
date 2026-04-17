package dashboard

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"phalanxsim2/internal/node"
)

const (
	recoverySampleInterval = 200 * time.Millisecond
	recoveryStableWindow   = 10 * time.Second
	recoveryEpochTimeout   = 5 * time.Minute
	recoveryHistoryMax     = 64
)

type RecoveryEpochView struct {
	EpochID        string                 `json:"epoch_id"`
	Trigger        string                 `json:"trigger"`
	StartedAtMS    int64                  `json:"started_at_ms"`
	EndedAtMS      *int64                 `json:"ended_at_ms,omitempty"`
	Status         string                 `json:"status"`
	NodesTotalAtT0 int                    `json:"nodes_total_at_t0"`
	LeadersAtT0    int                    `json:"leaders_at_t0"`
	Killed         RecoveryKilledView     `json:"killed"`
	Summary        RecoverySummaryView    `json:"summary"`
	Latency        RecoveryLatencySummary `json:"latency"`
	Churn          RecoveryChurnView      `json:"churn"`
}

type RecoveryKilledView struct {
	Total   int `json:"total"`
	Leaders int `json:"leaders"`
	Senders int `json:"senders"`
	Base    int `json:"base"`
}

type RecoverySummaryView struct {
	TimeToFirstNewLeaderMS          *int64  `json:"time_to_first_new_leader_ms"`
	TimeTo50ConnectedMS             *int64  `json:"time_to_50_connected_ms"`
	TimeTo90ConnectedMS             *int64  `json:"time_to_90_connected_ms"`
	TimeTo99ConnectedMS             *int64  `json:"time_to_99_connected_ms"`
	TopologyStableWindowElapsedMS   *int64  `json:"topology_stable_window_elapsed_ms"`
	TimeToTopologyStableWindowStart *int64  `json:"time_to_topology_stable_window_start_ms"`
	TimeToTopologyStableMS          *int64  `json:"time_to_topology_stable_ms"`
	PeakDisconnectedNodes           int     `json:"peak_disconnected_nodes"`
	OrphanNodeSeconds               float64 `json:"orphan_node_seconds"`
}

type RecoveryLatencySummary struct {
	NodeReconnectMS   RecoveryLatencyDist `json:"node_reconnect_ms"`
	LeaderPromotionMS RecoveryLatencyDist `json:"leader_promotion_ms"`
	ReparentMS        RecoveryLatencyDist `json:"reparent_ms"`
}

type RecoveryLatencyDist struct {
	Count      int    `json:"count"`
	SampleSize int    `json:"sample_size"`
	Min        *int64 `json:"min"`
	P50        *int64 `json:"p50"`
	P95        *int64 `json:"p95"`
	P99        *int64 `json:"p99"`
	Max        *int64 `json:"max"`
}

type RecoveryChurnView struct {
	LeaderChangesTotal int `json:"leader_changes_total"`
	ParentChangesTotal int `json:"parent_changes_total"`
	RoleFlapsTotal     int `json:"role_flaps_total"`
}

type roleTransition struct {
	From node.Role
	To   node.Role
}

type recoveryEpochState struct {
	view                 RecoveryEpochView
	startedAt            time.Time
	lastSampleAt         time.Time
	lastTopologyChangeAt time.Time
	persisted            bool

	baseRoleByNode      map[node.NodeID]node.Role
	lastRoleByNode      map[node.NodeID]node.Role
	lastParentByNode    map[node.NodeID]node.NodeID
	lastConnectedByNode map[node.NodeID]bool
	lastRoleTransition  map[node.NodeID]roleTransition
	reconnectSamplesMS  []int64
	promotionSamplesMS  []int64
	reparentSamplesMS   []int64
}

type recoverySample struct {
	at              time.Time
	total           int
	leaders         int
	connected       int
	disconnected    int
	orphanCount     int
	connectedByNode map[node.NodeID]bool
	roleByNode      map[node.NodeID]node.Role
	parentByNode    map[node.NodeID]node.NodeID
}

func (m *Manager) recoveryLoop() {
	defer m.runWG.Done()
	m.mu.Lock()
	runCtx := m.runCtx
	m.mu.Unlock()
	if runCtx == nil {
		return
	}

	ticker := time.NewTicker(recoverySampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			m.tickRecoveryEpoch()
		}
	}
}

func (m *Manager) startRecoveryEpoch(trigger string, killed RecoveryKilledView) {
	trigger = strings.TrimSpace(strings.ToLower(trigger))
	if trigger == "" {
		trigger = "kill"
	}

	sample, ok := m.captureRecoverySample(time.Now())
	if !ok {
		return
	}
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()
	if cfg.QFreezeAfterMS > 0 {
		m.applyQDriftFreeze(sample.at, time.Duration(cfg.QFreezeAfterMS)*time.Millisecond)
	}

	seq := m.recoverySeq.Add(1)
	epochID := fmt.Sprintf("ep-%s-%04d", sample.at.Format("20060102-150405"), seq)
	next := &recoveryEpochState{
		view: RecoveryEpochView{
			EpochID:        epochID,
			Trigger:        trigger,
			StartedAtMS:    sample.at.UnixMilli(),
			Status:         "running",
			NodesTotalAtT0: sample.total,
			LeadersAtT0:    sample.leaders,
			Killed:         killed,
		},
		startedAt:            sample.at,
		lastSampleAt:         sample.at,
		lastTopologyChangeAt: sample.at,
		baseRoleByNode:       copyRoleMap(sample.roleByNode),
		lastRoleByNode:       copyRoleMap(sample.roleByNode),
		lastParentByNode:     copyParentMap(sample.parentByNode),
		lastConnectedByNode:  copyBoolMap(sample.connectedByNode),
		lastRoleTransition:   make(map[node.NodeID]roleTransition),
	}

	m.recoveryMu.Lock()
	if cur := m.recoveryCurrent; cur != nil && cur.view.Status == "running" {
		cur.view.Status = "aborted"
		ts := sample.at.UnixMilli()
		cur.view.EndedAtMS = &ts
		m.finalizeRecoveryLocked(cur)
	}
	m.recoveryCurrent = next
	m.recoveryMu.Unlock()
}

func (m *Manager) tickRecoveryEpoch() {
	sample, ok := m.captureRecoverySample(time.Now())
	if !ok {
		return
	}

	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()
	ep := m.recoveryCurrent
	if ep == nil || ep.view.Status != "running" {
		return
	}

	deltaSec := sample.at.Sub(ep.lastSampleAt).Seconds()
	if deltaSec > 0 {
		ep.view.Summary.OrphanNodeSeconds += float64(sample.orphanCount) * deltaSec
	}
	ep.lastSampleAt = sample.at

	if sample.disconnected > ep.view.Summary.PeakDisconnectedNodes {
		ep.view.Summary.PeakDisconnectedNodes = sample.disconnected
	}
	m.setRecoveryConnectedThresholds(ep, sample)
	m.captureRecoveryLatencies(ep, sample)
	m.captureRecoveryTopologyStability(ep, sample)

	if ep.view.Summary.TimeToTopologyStableMS != nil {
		ep.view.Status = "stabilized"
		ts := sample.at.UnixMilli()
		ep.view.EndedAtMS = &ts
		m.finalizeRecoveryLocked(ep)
		return
	}
	if sample.at.Sub(ep.startedAt) >= recoveryEpochTimeout {
		ep.view.Status = "timeout"
		ts := sample.at.UnixMilli()
		ep.view.EndedAtMS = &ts
		m.finalizeRecoveryLocked(ep)
		return
	}
}

func (m *Manager) finalizeRecoveryLocked(ep *recoveryEpochState) {
	if ep == nil || ep.persisted {
		return
	}
	ep.view.Latency.NodeReconnectMS = buildRecoveryDist(ep.reconnectSamplesMS)
	ep.view.Latency.LeaderPromotionMS = buildRecoveryDist(ep.promotionSamplesMS)
	ep.view.Latency.ReparentMS = buildRecoveryDist(ep.reparentSamplesMS)
	m.recoveryHistory = append(m.recoveryHistory, ep.view)
	if len(m.recoveryHistory) > recoveryHistoryMax {
		m.recoveryHistory = append([]RecoveryEpochView(nil), m.recoveryHistory[len(m.recoveryHistory)-recoveryHistoryMax:]...)
	}
	ep.persisted = true
}

func (m *Manager) abortRecoveryEpoch() {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()
	if m.recoveryCurrent == nil || m.recoveryCurrent.view.Status != "running" {
		return
	}
	m.recoveryCurrent.view.Status = "aborted"
	ts := time.Now().UnixMilli()
	m.recoveryCurrent.view.EndedAtMS = &ts
	m.finalizeRecoveryLocked(m.recoveryCurrent)
}

func (m *Manager) RecoveryCurrent() (RecoveryEpochView, bool) {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()
	if m.recoveryCurrent == nil {
		return RecoveryEpochView{}, false
	}
	return m.recoveryCurrent.view, true
}

func (m *Manager) RecoveryEpochs(limit int) []RecoveryEpochView {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()

	if limit <= 0 {
		limit = 20
	}
	out := make([]RecoveryEpochView, 0, limit+1)
	if cur := m.recoveryCurrent; cur != nil {
		out = append(out, cur.view)
	}
	for i := len(m.recoveryHistory) - 1; i >= 0 && len(out) < limit; i-- {
		h := m.recoveryHistory[i]
		if len(out) > 0 && out[0].EpochID == h.EpochID {
			continue
		}
		out = append(out, h)
	}
	return out
}

func (m *Manager) RecoveryEpochByID(id string) (RecoveryEpochView, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return RecoveryEpochView{}, false
	}
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()
	if cur := m.recoveryCurrent; cur != nil && cur.view.EpochID == id {
		return cur.view, true
	}
	for i := len(m.recoveryHistory) - 1; i >= 0; i-- {
		if m.recoveryHistory[i].EpochID == id {
			return m.recoveryHistory[i], true
		}
	}
	return RecoveryEpochView{}, false
}

func (m *Manager) captureRecoverySample(ts time.Time) (recoverySample, bool) {
	m.mu.Lock()
	if !m.running || m.m == nil {
		m.mu.Unlock()
		return recoverySample{}, false
	}
	sim := m.m
	parentSnapshot := copyParentMap(m.parentByNode)
	m.mu.Unlock()

	nodes := sim.NodesSnapshot()
	s := recoverySample{
		at:              ts,
		connectedByNode: make(map[node.NodeID]bool, len(nodes)),
		roleByNode:      make(map[node.NodeID]node.Role, len(nodes)),
		parentByNode:    make(map[node.NodeID]node.NodeID, len(parentSnapshot)),
	}
	for _, n := range nodes {
		if n == nil || !n.IsRunning() {
			continue
		}
		s.total++
		s.roleByNode[n.ID] = n.Role
		if n.Role == node.RoleLeader {
			s.leaders++
		}

		connState := node.ConnStateSolo
		leaderID := node.NodeID(0)
		activeCount := 0
		if n.Manager != nil {
			ns := n.Manager.Snapshot()
			connState = ns.ConnectionState
			leaderID = ns.CurrentLeader.ID
			activeCount = len(ns.ActiveConnections)
		}
		if connState == node.ConnStateConnected {
			s.connected++
			s.connectedByNode[n.ID] = true
		}

		parentID := parentSnapshot[n.ID]
		if parentID == 0 && leaderID != 0 && leaderID != n.ID {
			parentID = leaderID
		}
		if parentID != 0 {
			s.parentByNode[n.ID] = parentID
		}

		isRootLeader := n.Role == node.RoleLeader && n.Level == 0
		hasLeaderPath := leaderID != 0
		hasActiveConn := activeCount > 0
		isOrphan := false
		switch n.Role {
		case node.RoleLeader:
			isOrphan = !isRootLeader && n.Level > 0 && !hasLeaderPath && !hasActiveConn
		default:
			isOrphan = !hasLeaderPath && !hasActiveConn
		}
		if isOrphan {
			s.orphanCount++
		}
	}
	if s.total < s.connected {
		s.connected = s.total
	}
	s.disconnected = s.total - s.connected
	return s, true
}

func (m *Manager) setRecoveryConnectedThresholds(ep *recoveryEpochState, sample recoverySample) {
	total := ep.view.NodesTotalAtT0
	if total <= 0 {
		return
	}
	setIfReached := func(dst **int64, thresholdPct float64) {
		if *dst != nil {
			return
		}
		need := int(math.Ceil(float64(total) * thresholdPct))
		if need < 1 {
			need = 1
		}
		if sample.connected >= need {
			ms := sample.at.Sub(ep.startedAt).Milliseconds()
			*dst = &ms
		}
	}
	setIfReached(&ep.view.Summary.TimeTo50ConnectedMS, 0.50)
	setIfReached(&ep.view.Summary.TimeTo90ConnectedMS, 0.90)
	setIfReached(&ep.view.Summary.TimeTo99ConnectedMS, 0.99)
}

func (m *Manager) captureRecoveryLatencies(ep *recoveryEpochState, sample recoverySample) {
	// reconnect: detect disconnected -> connected transitions.
	for id, connectedNow := range sample.connectedByNode {
		if !connectedNow {
			continue
		}
		connectedPrev := ep.lastConnectedByNode[id]
		if !connectedPrev {
			ep.reconnectSamplesMS = append(ep.reconnectSamplesMS, sample.at.Sub(ep.startedAt).Milliseconds())
		}
	}

	// promotion: detect non-leader -> leader transitions.
	for id, roleNow := range sample.roleByNode {
		rolePrev := ep.lastRoleByNode[id]
		if roleNow == node.RoleLeader && rolePrev != node.RoleLeader {
			ep.promotionSamplesMS = append(ep.promotionSamplesMS, sample.at.Sub(ep.startedAt).Milliseconds())
			if ep.view.Summary.TimeToFirstNewLeaderMS == nil && ep.baseRoleByNode[id] != node.RoleLeader {
				ms := sample.at.Sub(ep.startedAt).Milliseconds()
				ep.view.Summary.TimeToFirstNewLeaderMS = &ms
			}
		}
	}

	// reparent: detect parent changes where a new parent is known.
	for id, newParent := range sample.parentByNode {
		oldParent := ep.lastParentByNode[id]
		if newParent != 0 && newParent != oldParent {
			ep.reparentSamplesMS = append(ep.reparentSamplesMS, sample.at.Sub(ep.startedAt).Milliseconds())
		}
	}
}

func (m *Manager) captureRecoveryTopologyStability(ep *recoveryEpochState, sample recoverySample) {
	roleChanged := false
	for id, roleNow := range sample.roleByNode {
		rolePrev := ep.lastRoleByNode[id]
		if rolePrev != roleNow {
			roleChanged = true
			prevTx, ok := ep.lastRoleTransition[id]
			if ok && prevTx.From == roleNow && prevTx.To == rolePrev {
				ep.view.Churn.RoleFlapsTotal++
			}
			ep.lastRoleTransition[id] = roleTransition{From: rolePrev, To: roleNow}
			if roleNow == node.RoleLeader {
				ep.view.Churn.LeaderChangesTotal++
			}
		}
	}
	parentChanged := false
	if len(ep.lastParentByNode) != len(sample.parentByNode) {
		parentChanged = true
	}
	for id, parentNow := range sample.parentByNode {
		parentPrev := ep.lastParentByNode[id]
		if parentPrev != parentNow {
			parentChanged = true
			ep.view.Churn.ParentChangesTotal++
		}
	}
	for id, parentPrev := range ep.lastParentByNode {
		if _, ok := sample.parentByNode[id]; ok {
			continue
		}
		if parentPrev != 0 {
			parentChanged = true
			ep.view.Churn.ParentChangesTotal++
		}
	}
	if roleChanged || parentChanged {
		ep.lastTopologyChangeAt = sample.at
		zero := int64(0)
		ep.view.Summary.TopologyStableWindowElapsedMS = &zero
	}
	elapsedMS := sample.at.Sub(ep.lastTopologyChangeAt).Milliseconds()
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	ep.view.Summary.TopologyStableWindowElapsedMS = &elapsedMS
	ep.lastRoleByNode = copyRoleMap(sample.roleByNode)
	ep.lastParentByNode = copyParentMap(sample.parentByNode)
	ep.lastConnectedByNode = copyBoolMap(sample.connectedByNode)
	if ep.view.Summary.TimeToTopologyStableMS != nil {
		return
	}
	if sample.at.Sub(ep.lastTopologyChangeAt) >= recoveryStableWindow {
		windowStartMS := ep.lastTopologyChangeAt.Sub(ep.startedAt).Milliseconds()
		ep.view.Summary.TimeToTopologyStableWindowStart = &windowStartMS
		ms := sample.at.Sub(ep.startedAt).Milliseconds()
		ep.view.Summary.TimeToTopologyStableMS = &ms
	}
}

func buildRecoveryDist(samples []int64) RecoveryLatencyDist {
	out := RecoveryLatencyDist{
		Count:      len(samples),
		SampleSize: len(samples),
	}
	if len(samples) == 0 {
		return out
	}
	cp := append([]int64(nil), samples...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	min := cp[0]
	max := cp[len(cp)-1]
	p50 := percentileFromSorted(cp, 0.50)
	p95 := percentileFromSorted(cp, 0.95)
	out.Min = &min
	out.P50 = &p50
	out.P95 = &p95
	if len(cp) >= 100 {
		p99 := percentileFromSorted(cp, 0.99)
		out.P99 = &p99
	}
	out.Max = &max
	return out
}

func percentileFromSorted(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	idx := int(math.Ceil(float64(len(sorted))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func isNodeConnectedInSample(id node.NodeID, sample recoverySample) bool {
	return sample.connectedByNode[id]
}

func copyBoolMap(in map[node.NodeID]bool) map[node.NodeID]bool {
	if len(in) == 0 {
		return map[node.NodeID]bool{}
	}
	out := make(map[node.NodeID]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyParentMap(in map[node.NodeID]node.NodeID) map[node.NodeID]node.NodeID {
	if len(in) == 0 {
		return map[node.NodeID]node.NodeID{}
	}
	out := make(map[node.NodeID]node.NodeID, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyRoleMap(in map[node.NodeID]node.Role) map[node.NodeID]node.Role {
	if len(in) == 0 {
		return map[node.NodeID]node.Role{}
	}
	out := make(map[node.NodeID]node.Role, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func roleMapEqual(a, b map[node.NodeID]node.Role) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func parentMapEqual(a, b map[node.NodeID]node.NodeID) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
