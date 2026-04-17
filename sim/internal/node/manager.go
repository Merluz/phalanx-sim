package node

import (
	"sort"
	"sync"
)

// NodeManagerConfig controls local topology cache size.
type NodeManagerConfig struct {
	SameLevelN      int
	UpperLevelsY    int
	SenderThreshold float64
}

// DefaultNodeManagerConfig returns the baseline settings aligned with the
// current PHALANX draft defaults.
func DefaultNodeManagerConfig() NodeManagerConfig {
	return NodeManagerConfig{
		SameLevelN:      5,
		UpperLevelsY:    3,
		SenderThreshold: 0.7,
	}
}

// PeerInfo is the compact routing/topology descriptor exchanged in control
// flows and stored by local managers.
type PeerInfo struct {
	ID    NodeID  `json:"id"`
	IP    string  `json:"ip"`
	Q     float64 `json:"q"`
	Role  Role    `json:"role"`
	Level int     `json:"level"`
}

// UpperLevelView stores failover candidates for one known upper level:
// leader + same-level candidate nodes.
type UpperLevelView struct {
	Level      int        `json:"level"`
	Leader     PeerInfo   `json:"leader"`
	Candidates []PeerInfo `json:"candidates"`
}

// NodeManagerSnapshot is a serializable local manager view.
type NodeManagerSnapshot struct {
	Self              PeerInfo         `json:"self"`
	CurrentLevel      int              `json:"current_level"`
	TotalLevelsKnown  int              `json:"total_levels_known"`
	TopologyRevision  uint64           `json:"topology_revision"`
	ConnectionState   ConnState        `json:"connection_state"`
	CurrentLeader     PeerInfo         `json:"current_leader"`
	ActiveConnections []PeerInfo       `json:"active_connections"`
	SameLevel         []PeerInfo       `json:"same_level"`
	UpperLevels       []UpperLevelView `json:"upper_levels"`
	Failover          []PeerInfo       `json:"failover"`
}

// NodeManager keeps local role/topology state for one node.
type NodeManager struct {
	mu  sync.RWMutex
	cfg NodeManagerConfig

	self            PeerInfo
	currentLevel    int
	totalLevels     int
	topologyRev     uint64
	connectionState ConnState
	currentLeader   PeerInfo

	activeByID map[NodeID]PeerInfo
	sameLevel  []PeerInfo
	upper      []UpperLevelView
}

// NewNodeManager creates a manager bound to one node identity.
func NewNodeManager(self PeerInfo, cfg NodeManagerConfig) *NodeManager {
	if cfg.SameLevelN <= 0 {
		cfg.SameLevelN = 5
	}
	if cfg.UpperLevelsY <= 0 {
		cfg.UpperLevelsY = 3
	}
	if cfg.SenderThreshold < 0 {
		cfg.SenderThreshold = 0
	}
	if cfg.SenderThreshold > 1 {
		cfg.SenderThreshold = 1
	}
	return &NodeManager{
		cfg:             cfg,
		self:            self,
		currentLevel:    self.Level,
		totalLevels:     maxInt(1, self.Level+1),
		topologyRev:     0,
		connectionState: ConnStateSolo,
		activeByID:      make(map[NodeID]PeerInfo),
	}
}

func (m *NodeManager) Config() NodeManagerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *NodeManager) SetConnectionState(s ConnState) {
	m.mu.Lock()
	m.connectionState = s
	m.mu.Unlock()
}

func (m *NodeManager) UpdateSelfRole(role Role) {
	m.mu.Lock()
	m.self.Role = role
	m.mu.Unlock()
}

func (m *NodeManager) UpdateSelfLevel(level int) {
	m.mu.Lock()
	m.self.Level = level
	m.currentLevel = level
	if m.totalLevels < level+1 {
		m.totalLevels = level + 1
	}
	m.mu.Unlock()
}

func (m *NodeManager) UpdateSelfQ(q float64) {
	m.mu.Lock()
	m.self.Q = q
	m.mu.Unlock()
}

func (m *NodeManager) SetCurrentLeader(leader PeerInfo) {
	if leader.ID == 0 {
		return
	}
	m.mu.Lock()
	m.currentLeader = leader
	m.mu.Unlock()
}

// SetHierarchyPosition updates local hierarchy placement metadata.
// Root is level 0; total_levels is the known/estimated hierarchy height.
func (m *NodeManager) SetHierarchyPosition(level int, totalLevels int, revision uint64) {
	if level < 0 {
		level = 0
	}
	if totalLevels < level+1 {
		totalLevels = level + 1
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.topologyRev > 0 && revision > 0 && revision < m.topologyRev {
		return
	}
	if revision > 0 {
		m.topologyRev = revision
	}
	m.currentLevel = level
	m.totalLevels = totalLevels
	m.self.Level = level
}

func (m *NodeManager) ClearCurrentLeader() {
	m.mu.Lock()
	m.currentLeader = PeerInfo{}
	m.mu.Unlock()
}

func (m *NodeManager) UpsertActiveConnection(peer PeerInfo) {
	if peer.ID == 0 || peer.ID == m.self.ID {
		return
	}
	m.mu.Lock()
	m.activeByID[peer.ID] = peer
	m.mu.Unlock()
}

func (m *NodeManager) ClearActiveConnections() {
	m.mu.Lock()
	m.activeByID = make(map[NodeID]PeerInfo)
	m.mu.Unlock()
}

func (m *NodeManager) RemoveActiveConnection(id NodeID) {
	m.mu.Lock()
	delete(m.activeByID, id)
	m.mu.Unlock()
}

// SetSameLevel stores up to N same-level candidates ordered by Q desc and
// NodeID asc on tie.
func (m *NodeManager) SetSameLevel(peers []PeerInfo) {
	m.mu.Lock()
	m.sameLevel = normalizePeersLocked(peers, m.self.ID, m.cfg.SameLevelN)
	m.mu.Unlock()
}

// SetUpperLevel upserts one upper-level cache entry and keeps at most Y levels.
func (m *NodeManager) SetUpperLevel(level int, leader PeerInfo, candidates []PeerInfo) {
	if level <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	clean := UpperLevelView{
		Level:      level,
		Leader:     leader,
		Candidates: normalizePeersLocked(candidates, m.self.ID, m.cfg.SameLevelN),
	}

	updated := false
	for i := range m.upper {
		if m.upper[i].Level == level {
			m.upper[i] = clean
			updated = true
			break
		}
	}
	if !updated {
		m.upper = append(m.upper, clean)
	}

	sort.Slice(m.upper, func(i, j int) bool {
		return m.upper[i].Level < m.upper[j].Level
	})
	if len(m.upper) > m.cfg.UpperLevelsY {
		m.upper = append([]UpperLevelView(nil), m.upper[:m.cfg.UpperLevelsY]...)
	}
}

func (m *NodeManager) RemoveUpperLevel(level int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dst := m.upper[:0]
	for _, up := range m.upper {
		if up.Level == level {
			continue
		}
		dst = append(dst, up)
	}
	m.upper = append([]UpperLevelView(nil), dst...)
}

func (m *NodeManager) SetUpperLevels(levels []UpperLevelView) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(levels) == 0 {
		m.upper = nil
		return
	}
	normalized := make([]UpperLevelView, 0, len(levels))
	for _, up := range levels {
		if up.Level <= 0 {
			continue
		}
		normalized = append(normalized, UpperLevelView{
			Level:      up.Level,
			Leader:     up.Leader,
			Candidates: normalizePeersLocked(up.Candidates, m.self.ID, m.cfg.SameLevelN),
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Level < normalized[j].Level
	})
	if len(normalized) > m.cfg.UpperLevelsY {
		normalized = normalized[:m.cfg.UpperLevelsY]
	}
	m.upper = normalized
}

func (m *NodeManager) Snapshot() NodeManagerSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	active := make([]PeerInfo, 0, len(m.activeByID))
	for _, p := range m.activeByID {
		active = append(active, p)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })

	sameLevel := append([]PeerInfo(nil), m.sameLevel...)
	upper := make([]UpperLevelView, 0, len(m.upper))
	for _, up := range m.upper {
		candidates := append([]PeerInfo(nil), up.Candidates...)
		upper = append(upper, UpperLevelView{
			Level:      up.Level,
			Leader:     up.Leader,
			Candidates: candidates,
		})
	}

	return NodeManagerSnapshot{
		Self:              m.self,
		CurrentLevel:      m.currentLevel,
		TotalLevelsKnown:  m.totalLevels,
		TopologyRevision:  m.topologyRev,
		ConnectionState:   m.connectionState,
		CurrentLeader:     m.currentLeader,
		ActiveConnections: active,
		SameLevel:         sameLevel,
		UpperLevels:       upper,
		Failover:          m.buildFailoverLocked(),
	}
}

func (m *NodeManager) buildFailoverLocked() []PeerInfo {
	limit := m.cfg.SameLevelN + (m.cfg.UpperLevelsY * (m.cfg.SameLevelN + 1))
	if limit <= 0 {
		return nil
	}

	out := make([]PeerInfo, 0, limit)
	seen := make(map[NodeID]struct{}, limit)
	add := func(p PeerInfo) {
		if p.ID == 0 || p.ID == m.self.ID {
			return
		}
		if _, exists := seen[p.ID]; exists {
			return
		}
		seen[p.ID] = struct{}{}
		out = append(out, p)
	}

	for _, p := range m.sameLevel {
		if len(out) >= limit {
			return out
		}
		add(p)
	}

	for _, up := range m.upper {
		if len(out) >= limit {
			break
		}
		add(up.Leader)
		for _, p := range up.Candidates {
			if len(out) >= limit {
				break
			}
			add(p)
		}
	}
	return out
}

func normalizePeersLocked(peers []PeerInfo, selfID NodeID, limit int) []PeerInfo {
	if limit <= 0 || len(peers) == 0 {
		return nil
	}
	byID := make(map[NodeID]PeerInfo, len(peers))
	for _, p := range peers {
		if p.ID == 0 || p.ID == selfID {
			continue
		}
		byID[p.ID] = p
	}
	out := make([]PeerInfo, 0, len(byID))
	for _, p := range byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Q == out[j].Q {
			return out[i].ID < out[j].ID
		}
		return out[i].Q > out[j].Q
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
