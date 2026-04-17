package dashboard

import (
	"math"
	"runtime"
	"sort"
	"time"

	"phalanxsim2/internal/node"
)

// MetricsView is a compact runtime observability payload for CLI/UI polling.
type MetricsView struct {
	TSUnixMS       int64                     `json:"ts_unix_ms"`
	UptimeMS       int64                     `json:"uptime_ms"`
	SimPhase       string                    `json:"sim_phase"`
	Nodes          MetricsNodesView          `json:"nodes"`
	ConnState      MetricsConnStateView      `json:"conn_state"`
	Traffic        MetricsTrafficView        `json:"traffic"`
	PacketCounters MetricsPacketCountersView `json:"packet_counters"`
	Join           MetricsJoinView           `json:"join"`
	Kill           MetricsKillView           `json:"kill"`
	Topology       MetricsTopologyView       `json:"topology"`
	Perf           MetricsPerfView           `json:"perf"`
	Convergence    MetricsConvergenceView    `json:"convergence"`
}

type MetricsNodesView struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Leaders int `json:"leaders"`
	Senders int `json:"senders"`
	Base    int `json:"base"`
}

type MetricsConnStateView struct {
	Solo          int `json:"solo"`
	Joining       int `json:"joining"`
	Connected     int `json:"connected"`
	Transitioning int `json:"transitioning"`
	Disconnected  int `json:"disconnected"`
}

type MetricsTrafficView struct {
	DiscoverSentTotal uint64 `json:"discover_sent_total"`
	DiscoverRecvTotal uint64 `json:"discover_recv_total"`
	OfferSentTotal    uint64 `json:"offer_sent_total"`
	OfferRecvTotal    uint64 `json:"offer_recv_total"`
	ConnectSentTotal  uint64 `json:"connect_sent_total"`
}

type MetricsPacketCountersView struct {
	UDP map[string]MetricsPacketCounter `json:"udp"`
	WS  map[string]MetricsPacketCounter `json:"ws"`
}

type MetricsPacketCounter struct {
	Sent uint64 `json:"sent"`
	Recv uint64 `json:"recv"`
}

type MetricsJoinView struct {
	AttemptTotal uint64  `json:"attempt_total"`
	SuccessTotal uint64  `json:"success_total"`
	FailTotal    uint64  `json:"fail_total"`
	SuccessRate  float64 `json:"success_rate"`

	LatencyMSP50 int64 `json:"latency_ms_p50"`
	LatencyMSP95 int64 `json:"latency_ms_p95"`
	LatencyMSMax int64 `json:"latency_ms_max"`
}

type MetricsKillView struct {
	Total   uint64 `json:"total"`
	Leaders uint64 `json:"leaders"`
	Senders uint64 `json:"senders"`
	Base    uint64 `json:"base"`
}

type MetricsTopologyView struct {
	TotalLevels           int     `json:"total_levels"`
	Connections           int     `json:"connections"`
	AvgFollowersPerLeader float64 `json:"avg_followers_per_leader"`
	MaxFollowersPerLeader int     `json:"max_followers_per_leader"`
}

type MetricsPerfView struct {
	Goroutines           int     `json:"goroutines"`
	HeapAllocMB          float64 `json:"heap_alloc_mb"`
	APITopologyPayloadKB float64 `json:"api_topology_payload_kb"`
}

type MetricsConvergenceView struct {
	TimeToFirstLeaderMS    int64 `json:"time_to_first_leader_ms"`
	TimeTo90PctConnectedMS int64 `json:"time_to_90pct_connected_ms"`
	BootstrapTimeMS        int64 `json:"bootstrap_time_ms"`
}

func (m *Manager) resetMetricsState(start time.Time) {
	m.metricsMu.Lock()
	m.startedAt = start
	m.joinStarted = make(map[node.NodeID]time.Time)
	m.joinLatenciesMS = nil
	m.firstLeaderAt = time.Time{}
	m.first90ConnectedAt = time.Time{}
	m.stabilizedAt = time.Time{}
	m.metricsMu.Unlock()

	m.discoverSentTotal.Store(0)
	m.discoverRecvTotal.Store(0)
	m.offerSentTotal.Store(0)
	m.offerRecvTotal.Store(0)
	m.connectSentTotal.Store(0)
	m.joinAttemptTotal.Store(0)
	m.joinSuccessTotal.Store(0)
	m.joinFailTotal.Store(0)
	m.killTotal.Store(0)
	m.killLeaderTotal.Store(0)
	m.killSenderTotal.Store(0)
	m.killBaseTotal.Store(0)
	m.wsHelloSentTotal.Store(0)
	m.wsHelloRecvTotal.Store(0)
	m.wsHelloAckSentTotal.Store(0)
	m.wsHelloAckRecvTotal.Store(0)
	m.wsHelloRelaySentTotal.Store(0)
	m.wsHelloRelayRecvTotal.Store(0)
	m.wsTransitionHintSentTotal.Store(0)
	m.wsTransitionHintRecvTotal.Store(0)
	m.wsRotatePrepareSentTotal.Store(0)
	m.wsRotatePrepareRecvTotal.Store(0)
	m.wsRotatePrepareAckSentTotal.Store(0)
	m.wsRotatePrepareAckRecvTotal.Store(0)
	m.wsRotateCommitSentTotal.Store(0)
	m.wsRotateCommitRecvTotal.Store(0)
	m.wsRotateCommitAckSentTotal.Store(0)
	m.wsRotateCommitAckRecvTotal.Store(0)
	m.wsRotateAppliedSentTotal.Store(0)
	m.wsRotateAppliedRecvTotal.Store(0)
	m.lastTopologyPayloadBytes.Store(0)
}

func (m *Manager) setLastTopologyPayloadBytes(n int) {
	if n < 0 {
		n = 0
	}
	m.lastTopologyPayloadBytes.Store(int64(n))
}

func (m *Manager) addDiscoverSent(v uint64) {
	if v == 0 {
		return
	}
	m.discoverSentTotal.Add(v)
}

func (m *Manager) addDiscoverRecv(v uint64) {
	if v == 0 {
		return
	}
	m.discoverRecvTotal.Add(v)
}

func (m *Manager) addOfferSent(v uint64) {
	if v == 0 {
		return
	}
	m.offerSentTotal.Add(v)
}

func (m *Manager) addOfferRecv(v uint64) {
	if v == 0 {
		return
	}
	m.offerRecvTotal.Add(v)
}

func (m *Manager) addConnectSent(v uint64) {
	if v == 0 {
		return
	}
	m.connectSentTotal.Add(v)
}

func (m *Manager) addWSSentByType(t node.WSControlType) {
	switch t {
	case node.WSControlHello:
		m.wsHelloSentTotal.Add(1)
	case node.WSControlHelloAck:
		m.wsHelloAckSentTotal.Add(1)
	case node.WSControlHelloRelay:
		m.wsHelloRelaySentTotal.Add(1)
	case node.WSControlTransitionHint:
		m.wsTransitionHintSentTotal.Add(1)
	case node.WSControlRotatePrepare:
		m.wsRotatePrepareSentTotal.Add(1)
	case node.WSControlRotatePrepareAck:
		m.wsRotatePrepareAckSentTotal.Add(1)
	case node.WSControlRotateCommit:
		m.wsRotateCommitSentTotal.Add(1)
	case node.WSControlRotateCommitAck:
		m.wsRotateCommitAckSentTotal.Add(1)
	case node.WSControlRotateApplied:
		m.wsRotateAppliedSentTotal.Add(1)
	}
}

func (m *Manager) addWSRecvByType(t node.WSControlType) {
	switch t {
	case node.WSControlHello:
		m.wsHelloRecvTotal.Add(1)
	case node.WSControlHelloAck:
		m.wsHelloAckRecvTotal.Add(1)
	case node.WSControlHelloRelay:
		m.wsHelloRelayRecvTotal.Add(1)
	case node.WSControlTransitionHint:
		m.wsTransitionHintRecvTotal.Add(1)
	case node.WSControlRotatePrepare:
		m.wsRotatePrepareRecvTotal.Add(1)
	case node.WSControlRotatePrepareAck:
		m.wsRotatePrepareAckRecvTotal.Add(1)
	case node.WSControlRotateCommit:
		m.wsRotateCommitRecvTotal.Add(1)
	case node.WSControlRotateCommitAck:
		m.wsRotateCommitAckRecvTotal.Add(1)
	case node.WSControlRotateApplied:
		m.wsRotateAppliedRecvTotal.Add(1)
	}
}

func (m *Manager) markJoinAttempt(id node.NodeID) {
	if id == 0 {
		return
	}
	m.joinAttemptTotal.Add(1)
	m.metricsMu.Lock()
	if m.joinStarted == nil {
		m.joinStarted = make(map[node.NodeID]time.Time)
	}
	m.joinStarted[id] = time.Now()
	m.metricsMu.Unlock()
}

func (m *Manager) markJoinFail(id node.NodeID) {
	if id == 0 {
		return
	}
	m.joinFailTotal.Add(1)
	m.metricsMu.Lock()
	delete(m.joinStarted, id)
	m.metricsMu.Unlock()
}

func (m *Manager) markJoinSuccess(id node.NodeID) {
	if id == 0 {
		return
	}
	now := time.Now()
	m.metricsMu.Lock()
	start, ok := m.joinStarted[id]
	if ok {
		delete(m.joinStarted, id)
		ms := now.Sub(start).Milliseconds()
		const capLat = 4096
		if len(m.joinLatenciesMS) < capLat {
			m.joinLatenciesMS = append(m.joinLatenciesMS, ms)
		} else {
			copy(m.joinLatenciesMS, m.joinLatenciesMS[1:])
			m.joinLatenciesMS[len(m.joinLatenciesMS)-1] = ms
		}
	}
	m.metricsMu.Unlock()
	if ok {
		m.joinSuccessTotal.Add(1)
	}
}

func (m *Manager) addKillCounts(total, leaders, senders, base int) {
	if total > 0 {
		m.killTotal.Add(uint64(total))
	}
	if leaders > 0 {
		m.killLeaderTotal.Add(uint64(leaders))
	}
	if senders > 0 {
		m.killSenderTotal.Add(uint64(senders))
	}
	if base > 0 {
		m.killBaseTotal.Add(uint64(base))
	}
}

// Metrics returns a lightweight observability snapshot.
func (m *Manager) Metrics() MetricsView {
	now := time.Now()
	out := MetricsView{
		TSUnixMS: now.UnixMilli(),
		SimPhase: "idle",
	}

	out.Traffic = MetricsTrafficView{
		DiscoverSentTotal: m.discoverSentTotal.Load(),
		DiscoverRecvTotal: m.discoverRecvTotal.Load(),
		OfferSentTotal:    m.offerSentTotal.Load(),
		OfferRecvTotal:    m.offerRecvTotal.Load(),
		ConnectSentTotal:  m.connectSentTotal.Load(),
	}
	out.PacketCounters = MetricsPacketCountersView{
		UDP: map[string]MetricsPacketCounter{
			"discover": {Sent: m.discoverSentTotal.Load(), Recv: m.discoverRecvTotal.Load()},
			"offer":    {Sent: m.offerSentTotal.Load(), Recv: m.offerRecvTotal.Load()},
			"connect":  {Sent: m.connectSentTotal.Load(), Recv: 0},
		},
		WS: map[string]MetricsPacketCounter{
			"hello":                     {Sent: m.wsHelloSentTotal.Load(), Recv: m.wsHelloRecvTotal.Load()},
			"hello_ack":                 {Sent: m.wsHelloAckSentTotal.Load(), Recv: m.wsHelloAckRecvTotal.Load()},
			"hello_relay":               {Sent: m.wsHelloRelaySentTotal.Load(), Recv: m.wsHelloRelayRecvTotal.Load()},
			"transition_hint":           {Sent: m.wsTransitionHintSentTotal.Load(), Recv: m.wsTransitionHintRecvTotal.Load()},
			"leader_rotate_prepare":     {Sent: m.wsRotatePrepareSentTotal.Load(), Recv: m.wsRotatePrepareRecvTotal.Load()},
			"leader_rotate_prepare_ack": {Sent: m.wsRotatePrepareAckSentTotal.Load(), Recv: m.wsRotatePrepareAckRecvTotal.Load()},
			"leader_rotate_commit":      {Sent: m.wsRotateCommitSentTotal.Load(), Recv: m.wsRotateCommitRecvTotal.Load()},
			"leader_rotate_commit_ack":  {Sent: m.wsRotateCommitAckSentTotal.Load(), Recv: m.wsRotateCommitAckRecvTotal.Load()},
			"leader_rotate_applied":     {Sent: m.wsRotateAppliedSentTotal.Load(), Recv: m.wsRotateAppliedRecvTotal.Load()},
		},
	}

	attempts := m.joinAttemptTotal.Load()
	success := m.joinSuccessTotal.Load()
	fail := m.joinFailTotal.Load()
	out.Join.AttemptTotal = attempts
	out.Join.SuccessTotal = success
	out.Join.FailTotal = fail
	if attempts > 0 {
		out.Join.SuccessRate = float64(success) / float64(attempts)
	}
	out.Kill = MetricsKillView{
		Total:   m.killTotal.Load(),
		Leaders: m.killLeaderTotal.Load(),
		Senders: m.killSenderTotal.Load(),
		Base:    m.killBaseTotal.Load(),
	}

	m.mu.Lock()
	running := m.running
	spawnPhase := m.spawnPhase
	sim := m.m
	connections := 0
	for _, parentID := range m.parentByNode {
		if parentID != 0 {
			connections++
		}
	}
	totalFollowers := 0
	maxFollowers := 0
	for _, group := range m.followersByLeader {
		l := len(group)
		totalFollowers += l
		if l > maxFollowers {
			maxFollowers = l
		}
	}
	m.mu.Unlock()

	if !running || sim == nil {
		return out
	}

	nodes := sim.NodesSnapshot()
	maxLevel := 0
	for _, n := range nodes {
		if n == nil {
			continue
		}
		out.Nodes.Total++
		if n.IsRunning() {
			out.Nodes.Running++
		}
		switch n.Role {
		case node.RoleLeader:
			out.Nodes.Leaders++
		case node.RoleSender:
			out.Nodes.Senders++
		default:
			out.Nodes.Base++
		}
		if n.Level > maxLevel {
			maxLevel = n.Level
		}

		if n.Manager == nil {
			continue
		}
		s := n.Manager.Snapshot()
		switch s.ConnectionState {
		case node.ConnStateSolo:
			out.ConnState.Solo++
		case node.ConnStateJoining:
			out.ConnState.Joining++
		case node.ConnStateConnected:
			out.ConnState.Connected++
		case node.ConnStateTransitioning:
			out.ConnState.Transitioning++
		case node.ConnStateDisconnected:
			out.ConnState.Disconnected++
		}
	}

	out.Topology.Connections = connections
	out.Topology.TotalLevels = maxLevel + 1
	if out.Nodes.Leaders > 0 {
		out.Topology.AvgFollowersPerLeader = float64(totalFollowers) / float64(out.Nodes.Leaders)
	}
	out.Topology.MaxFollowersPerLeader = maxFollowers

	m.metricsMu.Lock()
	if m.startedAt.IsZero() {
		m.startedAt = now
	}
	if out.Nodes.Leaders > 0 && m.firstLeaderAt.IsZero() {
		m.firstLeaderAt = now
	}
	if out.Nodes.Total > 0 && (out.ConnState.Connected*100) >= (out.Nodes.Total*90) && m.first90ConnectedAt.IsZero() {
		m.first90ConnectedAt = now
	}
	if out.Nodes.Total > 0 && out.ConnState.Connected == out.Nodes.Total && out.Nodes.Leaders > 0 && m.stabilizedAt.IsZero() {
		m.stabilizedAt = now
	}
	startedAt := m.startedAt
	firstLeaderAt := m.firstLeaderAt
	first90At := m.first90ConnectedAt
	stabilizedAt := m.stabilizedAt
	lat := append([]int64(nil), m.joinLatenciesMS...)
	m.metricsMu.Unlock()

	out.SimPhase = inferSimPhase(spawnPhase, out.Nodes.Total, out.Nodes.Leaders, out.ConnState.Connected)
	if !startedAt.IsZero() {
		out.UptimeMS = now.Sub(startedAt).Milliseconds()
	}
	if !firstLeaderAt.IsZero() {
		out.Convergence.TimeToFirstLeaderMS = firstLeaderAt.Sub(startedAt).Milliseconds()
	}
	if !first90At.IsZero() {
		out.Convergence.TimeTo90PctConnectedMS = first90At.Sub(startedAt).Milliseconds()
	}
	if !stabilizedAt.IsZero() {
		out.Convergence.BootstrapTimeMS = stabilizedAt.Sub(startedAt).Milliseconds()
	}

	if len(lat) > 0 {
		out.Join.LatencyMSP50 = percentileInt64(lat, 0.50)
		out.Join.LatencyMSP95 = percentileInt64(lat, 0.95)
		out.Join.LatencyMSMax = maxInt64Slice(lat)
	}

	out.Perf.Goroutines = runtime.NumGoroutine()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out.Perf.HeapAllocMB = float64(ms.HeapAlloc) / (1024.0 * 1024.0)
	out.Perf.APITopologyPayloadKB = float64(m.lastTopologyPayloadBytes.Load()) / 1024.0

	return out
}

func inferSimPhase(spawnPhase string, totalNodes int, leaders int, connected int) string {
	if spawnPhase == "spawning" {
		return "spawning"
	}
	if totalNodes == 0 {
		return "idle"
	}
	if leaders == 0 {
		return "discovery"
	}
	if connected < totalNodes {
		return "joining"
	}
	return "stabilized"
}

func percentileInt64(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	cp := append([]int64(nil), values...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(math.Ceil(float64(len(cp))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func maxInt64Slice(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	maxV := values[0]
	for i := 1; i < len(values); i++ {
		if values[i] > maxV {
			maxV = values[i]
		}
	}
	return maxV
}
