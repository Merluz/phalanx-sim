package dashboard

import (
	"sort"
	"strings"

	"phalanxsim2/internal/node"
)

// NodesQuery configures /api/nodes filtering and pagination.
type NodesQuery struct {
	Limit    int
	Offset   int
	Role     string
	State    string
	Level    int
	HasLevel bool
	SortBy   string
	Order    string
}

// NodesView is the paginated response for /api/nodes.
type NodesView struct {
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Items  []NodeListItem `json:"items"`
}

// NodeListItem is a compact node row suitable for table/list UIs.
type NodeListItem struct {
	ID               node.NodeID `json:"id"`
	IP               string      `json:"ip"`
	Role             string      `json:"role"`
	Level            int         `json:"level"`
	TopologyID       string      `json:"topology_id"`
	ParentID         node.NodeID `json:"parent_id"`
	LeaderID         node.NodeID `json:"leader_id"`
	ConnectionState  string      `json:"connection_state"`
	ActiveLinksCount int         `json:"active_links_count"`
	FailoverCount    int         `json:"failover_count"`
	Q                float64     `json:"q"`
	Running          bool        `json:"running"`
	Subnet           uint64      `json:"subnet"`
	TopologyRevision uint64      `json:"topology_revision"`
	TotalLevels      int         `json:"total_levels"`
}

func normalizeNodesQuery(q NodesQuery) NodesQuery {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > 2000 {
		q.Limit = 2000
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	q.Role = strings.ToLower(strings.TrimSpace(q.Role))
	if q.Role == "" {
		q.Role = "all"
	}
	q.State = strings.ToLower(strings.TrimSpace(q.State))
	if q.State == "" {
		q.State = "all"
	}
	q.SortBy = strings.ToLower(strings.TrimSpace(q.SortBy))
	if q.SortBy == "" {
		q.SortBy = "id"
	}
	q.Order = strings.ToLower(strings.TrimSpace(q.Order))
	if q.Order == "" {
		q.Order = "asc"
	}
	return q
}

func matchesNodeRole(v NodeListItem, role string) bool {
	if role == "all" {
		return true
	}
	return v.Role == role
}

func matchesNodeState(v NodeListItem, state string) bool {
	if state == "all" {
		return true
	}
	return v.ConnectionState == state
}

func nodeLess(items []NodeListItem, i, j int, sortBy string) bool {
	a := items[i]
	b := items[j]
	switch sortBy {
	case "q":
		if a.Q == b.Q {
			return a.ID < b.ID
		}
		return a.Q < b.Q
	case "level":
		if a.Level == b.Level {
			return a.ID < b.ID
		}
		return a.Level < b.Level
	case "active_links":
		if a.ActiveLinksCount == b.ActiveLinksCount {
			return a.ID < b.ID
		}
		return a.ActiveLinksCount < b.ActiveLinksCount
	default:
		return a.ID < b.ID
	}
}

// Nodes returns filtered, sorted and paginated nodes for debug APIs.
func (m *Manager) Nodes(q NodesQuery) NodesView {
	q = normalizeNodesQuery(q)

	m.mu.Lock()
	defer m.mu.Unlock()

	out := NodesView{
		Limit:  q.Limit,
		Offset: q.Offset,
		Items:  []NodeListItem{},
	}

	if !m.running || m.m == nil {
		return out
	}

	subnetByNode := map[node.NodeID]uint64{}
	if m.fabric != nil {
		fsnap := m.fabric.Snapshot()
		subnetByNode = make(map[node.NodeID]uint64, len(fsnap.Nodes))
		for _, ep := range fsnap.Nodes {
			subnetByNode[ep.NodeID] = ep.SubnetID
		}
	}

	filtered := make([]NodeListItem, 0, len(m.m.NodesSnapshot()))
	for _, n := range m.m.NodesSnapshot() {
		if n == nil {
			continue
		}
		level := n.Level
		connState := node.ConnStateSolo.String()
		leaderID := node.NodeID(0)
		failoverCount := 0
		topologyRev := uint64(0)
		totalLevels := 0
		activeLinksCount := 0
		if n.Manager != nil {
			s := n.Manager.Snapshot()
			level = s.CurrentLevel
			connState = s.ConnectionState.String()
			leaderID = s.CurrentLeader.ID
			failoverCount = len(s.Failover)
			topologyRev = s.TopologyRevision
			totalLevels = s.TotalLevelsKnown
			activeLinksCount = len(s.ActiveConnections)
		}

		item := NodeListItem{
			ID:               n.ID,
			IP:               n.IP,
			Role:             n.Role.String(),
			Level:            level,
			TopologyID:       m.topologyByNode[n.ID],
			ParentID:         m.parentByNode[n.ID],
			LeaderID:         leaderID,
			ConnectionState:  connState,
			ActiveLinksCount: activeLinksCount,
			FailoverCount:    failoverCount,
			Q:                n.Metrics.Q,
			Running:          n.IsRunning(),
			Subnet:           subnetByNode[n.ID],
			TopologyRevision: topologyRev,
			TotalLevels:      totalLevels,
		}
		if !matchesNodeRole(item, q.Role) {
			continue
		}
		if !matchesNodeState(item, q.State) {
			continue
		}
		if q.HasLevel && item.Level != q.Level {
			continue
		}
		filtered = append(filtered, item)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if q.Order == "desc" {
			return nodeLess(filtered, j, i, q.SortBy)
		}
		return nodeLess(filtered, i, j, q.SortBy)
	})

	out.Total = len(filtered)
	if q.Offset >= len(filtered) {
		return out
	}
	end := q.Offset + q.Limit
	if end > len(filtered) {
		end = len(filtered)
	}
	out.Items = filtered[q.Offset:end]
	return out
}
