package dashboard

import (
	"context"
	"errors"
	"log"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"phalanxsim2/internal/hierarchy"
	"phalanxsim2/internal/mesh"
	"phalanxsim2/internal/node"
	"phalanxsim2/internal/node/connsim"
)

// NodeView is the node shape returned to the UI.
type NodeView struct {
	ID               node.NodeID   `json:"id"`
	IP               string        `json:"ip"`
	Role             string        `json:"role"`
	Level            int           `json:"level"`
	TopologyID       string        `json:"topology_id"`
	ParentID         node.NodeID   `json:"parent_id"`
	ParentTopologyID string        `json:"parent_topology_id"`
	PathToRoot       []string      `json:"path_to_root,omitempty"`
	ChildLeaders     []node.NodeID `json:"child_leaders,omitempty"`
	Followers        []node.NodeID `json:"followers,omitempty"`
	ActiveLinks      []node.NodeID `json:"active_links,omitempty"`
	TotalLevels      int           `json:"total_levels"`
	TopologyRev      uint64        `json:"topology_revision"`
	Running          bool          `json:"running"`
	Subnet           uint64        `json:"subnet"`
	Q                float64       `json:"q"`
	ConnectionState  string        `json:"connection_state"`
	LeaderID         node.NodeID   `json:"leader_id"`
	FailoverCount    int           `json:"failover_count"`
}

// TopologyView is the topology payload for frontend rendering.
type TopologyView struct {
	Nodes       []NodeView              `json:"nodes"`
	Edges       []connsim.EdgeSnapshot  `json:"edges"`
	Connections []LogicalConnectionView `json:"connections,omitempty"`
}

// LogicalConnectionView is an explicit parent/leader topology connection.
type LogicalConnectionView struct {
	From           node.NodeID `json:"from"`
	To             node.NodeID `json:"to"`
	FromTopologyID string      `json:"from_topology_id"`
	ToTopologyID   string      `json:"to_topology_id"`
	TopologyPath   []string    `json:"topology_path,omitempty"`
	Kind           string      `json:"kind"`
}

// StateView is the runtime state payload.
type StateView struct {
	Running          bool        `json:"running"`
	Config           StartConfig `json:"config"`
	Stats            mesh.Stats  `json:"stats"`
	SpawnPhase       string      `json:"spawn_phase"`
	SpawnedNodes     int         `json:"spawned_nodes"`
	SpawnTotal       int         `json:"spawn_total"`
	SpawnProgressPct float64     `json:"spawn_progress_pct"`
}

// Manager owns one running simulation instance at a time.
type Manager struct {
	mu sync.Mutex
	// placementMu serializes topology mutations (join placement, rotations).
	placementMu sync.Mutex

	running      bool
	cfg          StartConfig
	stats        mesh.Stats
	spawnPhase   string
	spawnedNodes int
	spawnTotal   int

	m        *mesh.Mesh
	fabric   *connsim.Fabric
	wsFabric *connsim.WSFabric
	hier     *hierarchy.State

	runCtx    context.Context
	cancelRun context.CancelFunc
	runWG     sync.WaitGroup

	traceSeq atomic.Uint64

	discRng      *rand.Rand
	discRngMu    sync.Mutex
	discMu       sync.Mutex
	discState    map[node.NodeID]discoveryBackoffState
	offerTB      map[node.NodeID]offerTokenBucket
	reparentAt   map[node.NodeID]time.Time
	qFreezeUntil time.Time

	// Discovery/connection state:
	// connected[from] = to
	connected  map[node.NodeID]node.NodeID
	rootNodeID node.NodeID

	levelRotation  map[int]*levelRotationState
	rotationPrep   map[string]*rotationPrepareProposal
	rotationCommit map[string]*rotationPrepareProposal

	// Explicit topology maps (tree-shaped).
	topologyByNode     map[node.NodeID]string
	parentByNode       map[node.NodeID]node.NodeID
	childLeadersByNode map[node.NodeID][]node.NodeID
	followersByLeader  map[node.NodeID][]node.NodeID

	metricsMu          sync.Mutex
	startedAt          time.Time
	joinStarted        map[node.NodeID]time.Time
	joinLatenciesMS    []int64
	firstLeaderAt      time.Time
	first90ConnectedAt time.Time
	stabilizedAt       time.Time

	discoverSentTotal atomic.Uint64
	discoverRecvTotal atomic.Uint64
	offerSentTotal    atomic.Uint64
	offerRecvTotal    atomic.Uint64
	connectSentTotal  atomic.Uint64
	joinAttemptTotal  atomic.Uint64
	joinSuccessTotal  atomic.Uint64
	joinFailTotal     atomic.Uint64
	killTotal         atomic.Uint64
	killLeaderTotal   atomic.Uint64
	killSenderTotal   atomic.Uint64
	killBaseTotal     atomic.Uint64

	wsHelloSentTotal            atomic.Uint64
	wsHelloRecvTotal            atomic.Uint64
	wsHelloAckSentTotal         atomic.Uint64
	wsHelloAckRecvTotal         atomic.Uint64
	wsHelloRelaySentTotal       atomic.Uint64
	wsHelloRelayRecvTotal       atomic.Uint64
	wsTransitionHintSentTotal   atomic.Uint64
	wsTransitionHintRecvTotal   atomic.Uint64
	wsRotatePrepareSentTotal    atomic.Uint64
	wsRotatePrepareRecvTotal    atomic.Uint64
	wsRotatePrepareAckSentTotal atomic.Uint64
	wsRotatePrepareAckRecvTotal atomic.Uint64
	wsRotateCommitSentTotal     atomic.Uint64
	wsRotateCommitRecvTotal     atomic.Uint64
	wsRotateCommitAckSentTotal  atomic.Uint64
	wsRotateCommitAckRecvTotal  atomic.Uint64
	wsRotateAppliedSentTotal    atomic.Uint64
	wsRotateAppliedRecvTotal    atomic.Uint64

	lastTopologyPayloadBytes atomic.Int64

	recoveryMu      sync.Mutex
	recoverySeq     atomic.Uint64
	recoveryCurrent *recoveryEpochState
	recoveryHistory []RecoveryEpochView
}

type levelRotationState struct {
	ObservedLeader node.NodeID
	LeaderSince    time.Time
	TriggerSince   time.Time
	LastRotation   time.Time
}

type discoveryBackoffState struct {
	Attempt int
	NextAt  time.Time
}

type offerTokenBucket struct {
	Tokens float64
	Last   time.Time
}

type rotationPrepareProposal struct {
	mu sync.Mutex

	expected  map[node.NodeID]struct{}
	responded map[node.NodeID]bool
	accepted  map[node.NodeID]struct{}
	rejected  map[node.NodeID]string
	quorum    int
	done      chan struct{}
	doneOnce  sync.Once
}

// NewManager creates an idle simulation manager.
func NewManager() *Manager {
	return &Manager{
		cfg:        DefaultStartConfig(),
		spawnPhase: "idle",
	}
}

// Start stops any existing run and starts a new one with cfg.
func (m *Manager) Start(cfg StartConfig) error {
	if err := validateStartConfig(&cfg); err != nil {
		return err
	}

	if err := m.Stop(); err != nil {
		return err
	}

	meshCfg := mesh.DefaultConfig()
	meshCfg.N = cfg.N
	meshCfg.Y = cfg.Y
	meshCfg.TickPeriod = time.Duration(cfg.TickMS) * time.Millisecond
	meshCfg.InboxSize = cfg.InboxSize
	meshCfg.SubnetSize = cfg.SubnetSize
	meshCfg.NodeGoroutines = cfg.NodeGoroutines
	meshCfg.AsyncSpawn = cfg.AsyncSpawn
	meshCfg.SpawnParallelism = cfg.SpawnParallelism
	meshCfg.SpawnDelay = time.Duration(cfg.SpawnDelayMS) * time.Millisecond
	meshCfg.QDriftMaxPerSec = cfg.QDriftPctPerSec
	meshCfg.QUpdateInterval = time.Duration(cfg.QUpdateIntervalMS) * time.Millisecond

	seed := cfg.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
		cfg.Seed = seed
	}
	rng := rand.New(rand.NewSource(seed))

	runCtx, cancel := context.WithCancel(context.Background())
	sim := mesh.New(meshCfg, rng)

	fcfg := connsim.DefaultConfig()
	fcfg.MinDelay = time.Duration(cfg.UDPDelayMinMS) * time.Millisecond
	fcfg.MaxDelay = time.Duration(cfg.UDPDelayMaxMS) * time.Millisecond
	fcfg.DropRate = cfg.UDPDropRate
	fabric := connsim.NewFabric(fcfg, rand.New(rand.NewSource(seed+99)))
	wcfg := connsim.DefaultWSConfig()
	wcfg.MinDelay = fcfg.MinDelay
	wcfg.MaxDelay = fcfg.MaxDelay
	wcfg.DropRate = fcfg.DropRate
	wsFabric := connsim.NewWSFabric(wcfg, rand.New(rand.NewSource(seed+199)))
	hier := hierarchy.NewState(cfg.N)

	// Publish runtime state before spawn so UI can observe progressive node
	// creation while /api/start is still in-flight.
	m.mu.Lock()
	m.running = true
	m.cfg = cfg
	m.m = sim
	m.fabric = fabric
	m.wsFabric = wsFabric
	m.hier = hier
	m.stats = sim.Stats()
	m.runCtx = runCtx
	m.cancelRun = cancel
	m.discRng = rand.New(rand.NewSource(seed + 2026))
	m.discState = make(map[node.NodeID]discoveryBackoffState)
	m.offerTB = make(map[node.NodeID]offerTokenBucket)
	m.reparentAt = make(map[node.NodeID]time.Time)
	m.qFreezeUntil = time.Time{}
	m.connected = make(map[node.NodeID]node.NodeID)
	m.rootNodeID = 0
	m.levelRotation = make(map[int]*levelRotationState)
	m.rotationPrep = make(map[string]*rotationPrepareProposal)
	m.rotationCommit = make(map[string]*rotationPrepareProposal)
	m.topologyByNode = make(map[node.NodeID]string)
	m.parentByNode = make(map[node.NodeID]node.NodeID)
	m.childLeadersByNode = make(map[node.NodeID][]node.NodeID)
	m.followersByLeader = make(map[node.NodeID][]node.NodeID)
	m.spawnPhase = "spawning"
	m.spawnedNodes = 0
	m.spawnTotal = cfg.Nodes
	m.resetMetricsState(time.Now())
	m.recoveryMu.Lock()
	m.recoveryCurrent = nil
	m.recoveryMu.Unlock()
	m.mu.Unlock()

	// For small spawns we keep discovery active while nodes are joining.
	// For larger spawns we defer UDP discovery until bootstrap completes to
	// avoid startup storms and reduce spawn latency jitter.
	udpLoopStarted := false
	if cfg.UDPDemo && shouldRunLiveDiscoveryDuringSpawn(cfg) {
		m.runWG.Add(1)
		go m.udpLoop()
		udpLoopStarted = true
	}
	m.runWG.Add(1)
	go m.statsLoop()
	m.runWG.Add(1)
	go m.recoveryLoop()
	if cfg.LeaderRotation {
		m.runWG.Add(1)
		go m.rotationLoop()
	}

	// Spawn + register progressively so delay/asynchrony are visible in logs and runtime.
	if err := m.spawnAndRegisterNodes(runCtx, sim, fabric, wsFabric, cfg); err != nil {
		_ = m.Stop()
		return err
	}
	if cfg.UDPDemo && !udpLoopStarted {
		m.runWG.Add(1)
		go m.udpLoop()
	}
	_ = m.rebuildHierarchy()
	m.mu.Lock()
	m.stats = sim.Stats()
	m.spawnPhase = "running"
	m.spawnedNodes = cfg.Nodes
	m.spawnTotal = cfg.Nodes
	m.mu.Unlock()

	return nil
}

// Stop terminates the current run if present.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}

	cancel := m.cancelRun
	sim := m.m
	timeout := time.Duration(m.cfg.ShutdownTimeoutMS) * time.Millisecond
	m.running = false
	m.stats = mesh.Stats{}
	m.spawnPhase = "idle"
	m.spawnedNodes = 0
	m.spawnTotal = 0
	m.m = nil
	m.fabric = nil
	m.wsFabric = nil
	m.hier = nil
	m.runCtx = nil
	m.cancelRun = nil
	m.discRng = nil
	m.discState = nil
	m.offerTB = nil
	m.reparentAt = nil
	m.qFreezeUntil = time.Time{}
	m.connected = nil
	m.rootNodeID = 0
	m.levelRotation = nil
	m.rotationPrep = nil
	m.rotationCommit = nil
	m.topologyByNode = nil
	m.parentByNode = nil
	m.childLeadersByNode = nil
	m.followersByLeader = nil
	m.mu.Unlock()

	m.abortRecoveryEpoch()

	if cancel != nil {
		cancel()
	}
	m.runWG.Wait()
	if sim != nil {
		_ = sim.Shutdown(timeout)
	}
	return nil
}

// Defaults returns UI defaults.
func (m *Manager) Defaults() StartConfig {
	return DefaultStartConfig()
}

// State returns current manager state.
func (m *Manager) State() StateView {
	m.mu.Lock()
	defer m.mu.Unlock()
	pct := 0.0
	if m.spawnTotal > 0 {
		pct = (float64(m.spawnedNodes) / float64(m.spawnTotal)) * 100.0
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
	}
	return StateView{
		Running:          m.running,
		Config:           m.cfg,
		Stats:            m.stats,
		SpawnPhase:       m.spawnPhase,
		SpawnedNodes:     m.spawnedNodes,
		SpawnTotal:       m.spawnTotal,
		SpawnProgressPct: pct,
	}
}

// Topology returns nodes + observed traffic edges.
func (m *Manager) Topology() TopologyView {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.m == nil || m.fabric == nil {
		return TopologyView{}
	}

	fsnap := m.fabric.Snapshot()
	edges := limitEdgeSnapshotsForAPI(fsnap.Edges, 12000)
	subnetByNode := make(map[node.NodeID]uint64, len(fsnap.Nodes))
	for _, n := range fsnap.Nodes {
		subnetByNode[n.NodeID] = n.SubnetID
	}

	nodes := make([]NodeView, 0, len(fsnap.Nodes))
	for _, n := range m.m.NodesSnapshot() {
		role := n.Role.String()
		connState := node.ConnStateSolo.String()
		var leaderID node.NodeID
		failoverCount := 0
		level := n.Level
		totalLevels := 0
		topologyRev := uint64(0)
		activeLinks := make([]node.NodeID, 0)
		if n.Manager != nil {
			snap := n.Manager.Snapshot()
			level = snap.CurrentLevel
			connState = snap.ConnectionState.String()
			leaderID = snap.CurrentLeader.ID
			failoverCount = len(snap.Failover)
			totalLevels = snap.TotalLevelsKnown
			topologyRev = snap.TopologyRevision
			for _, p := range snap.ActiveConnections {
				activeLinks = append(activeLinks, p.ID)
			}
			sort.Slice(activeLinks, func(i, j int) bool { return activeLinks[i] < activeLinks[j] })
		}
		topoID := m.topologyByNode[n.ID]
		parentID := m.parentByNode[n.ID]
		parentTopoID := m.topologyByNode[parentID]
		pathToRoot := buildTopologyPathToRoot(n.ID, m.parentByNode, m.topologyByNode)
		childLeaders := append([]node.NodeID(nil), m.childLeadersByNode[n.ID]...)
		sort.Slice(childLeaders, func(i, j int) bool { return childLeaders[i] < childLeaders[j] })
		followers := append([]node.NodeID(nil), m.followersByLeader[n.ID]...)
		sort.Slice(followers, func(i, j int) bool { return followers[i] < followers[j] })
		nodes = append(nodes, NodeView{
			ID:               n.ID,
			IP:               n.IP,
			Role:             role,
			Level:            level,
			TopologyID:       topoID,
			ParentID:         parentID,
			ParentTopologyID: parentTopoID,
			PathToRoot:       pathToRoot,
			ChildLeaders:     childLeaders,
			Followers:        followers,
			ActiveLinks:      activeLinks,
			TotalLevels:      totalLevels,
			TopologyRev:      topologyRev,
			Running:          n.IsRunning(),
			Subnet:           subnetByNode[n.ID],
			Q:                n.Metrics.Q,
			ConnectionState:  connState,
			LeaderID:         leaderID,
			FailoverCount:    failoverCount,
		})
	}

	connections := make([]LogicalConnectionView, 0, len(m.parentByNode))
	for from, to := range m.parentByNode {
		if from == 0 || to == 0 {
			continue
		}
		connections = append(connections, LogicalConnectionView{
			From:           from,
			To:             to,
			FromTopologyID: m.topologyByNode[from],
			ToTopologyID:   m.topologyByNode[to],
			TopologyPath:   buildTopologyPathToRoot(from, m.parentByNode, m.topologyByNode),
			Kind:           "parent",
		})
	}
	sort.Slice(connections, func(i, j int) bool {
		if connections[i].From == connections[j].From {
			return connections[i].To < connections[j].To
		}
		return connections[i].From < connections[j].From
	})
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	return TopologyView{
		Nodes:       nodes,
		Edges:       edges,
		Connections: connections,
	}
}

// ResetEdges clears historical observed traffic edges in the active simulation.
func (m *Manager) ResetEdges() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running || m.fabric == nil {
		return nil
	}
	m.fabric.ResetEdges()
	return nil
}

func (m *Manager) udpLoop() {
	defer m.runWG.Done()
	m.mu.Lock()
	runCtx := m.runCtx
	m.mu.Unlock()
	if runCtx == nil {
		return
	}
	ticker := time.NewTicker(time.Duration(m.cfg.UDPIntervalMS) * time.Millisecond)
	defer ticker.Stop()
	runTick := func() {
		m.mu.Lock()
		if !m.running || m.m == nil || m.fabric == nil {
			m.mu.Unlock()
			return
		}
		cfg := m.cfg
		fabric := m.fabric
		m.mu.Unlock()

		if cfg.UDPSender == "root" {
			rootID := m.ensureRootNodeID()
			if rootID == 0 {
				return
			}
			ids := m.unconnectedNodeIDs()
			m.shuffleNodeIDs(ids)
			for _, sender := range ids {
				if sender == 0 || sender == rootID {
					continue
				}
				trace := connsim.NewTraceID("udp", m.traceSeq.Add(1))
				if fabric.Send(sender, rootID, cfg.UDPKind, []byte(cfg.UDPPayload), trace) {
					m.addDiscoverSent(1)
				}
			}
			return
		}

		// Random mode: every not-yet-connected endpoint sends limited discovery probes.
		ids := m.unconnectedNodeIDs()
		m.shuffleNodeIDs(ids)
		now := time.Now()
		m.reconcileDiscoveryState(ids)
		allIDs := fabricNodeIDs(fabric)
		for _, sender := range ids {
			if !m.canSendDiscoveryNow(sender, now) {
				continue
			}
			targets := m.pickRandomTargets(sender, allIDs, cfg.RandomDiscoverFanout)
			sent := m.sendDiscoverBurst(fabric, sender, targets, cfg.UDPKind, []byte(cfg.UDPPayload))
			m.updateDiscoveryBackoff(sender, sent, now, cfg)
		}
	}

	// Run one immediate discovery tick to avoid a dead window before the first
	// ticker event, especially visible when spawn_delay_ms > 0.
	runTick()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			runTick()
		}
	}
}

func (m *Manager) statsLoop() {
	defer m.runWG.Done()
	m.mu.Lock()
	runCtx := m.runCtx
	m.mu.Unlock()
	if runCtx == nil {
		return
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.running && m.m != nil {
				m.stats = m.m.Stats()
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) rotationLoop() {
	defer m.runWG.Done()
	m.mu.Lock()
	checkEvery := time.Duration(m.cfg.RotationCheckMS) * time.Millisecond
	runCtx := m.runCtx
	m.mu.Unlock()
	if checkEvery <= 0 {
		checkEvery = 1 * time.Second
	}
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			m.evaluateLeaderRotation()
		}
	}
}

func (m *Manager) evaluateLeaderRotation() {
	m.mu.Lock()
	if !m.running || m.m == nil || m.hier == nil || !m.cfg.LeaderRotation {
		m.mu.Unlock()
		return
	}
	cfg := m.cfg
	hier := m.hier
	if m.levelRotation == nil {
		m.levelRotation = make(map[int]*levelRotationState)
	}
	m.mu.Unlock()

	snap := hier.Snapshot()
	now := time.Now()
	window := time.Duration(cfg.RotationWindowMS) * time.Millisecond
	minTenure := time.Duration(cfg.RotationMinTenureMS) * time.Millisecond
	cooldown := time.Duration(cfg.RotationCooldownMS) * time.Millisecond

	for _, lv := range snap.Levels {
		if lv.Leader.ID == 0 {
			continue
		}
		others := make([]node.PeerInfo, 0, len(lv.Candidates)+len(lv.Overflow))
		others = append(others, lv.Candidates...)
		others = append(others, lv.Overflow...)
		if len(others) == 0 {
			m.resetRotationTrigger(lv.Level, lv.Leader.ID, now)
			continue
		}
		sort.Slice(others, func(i, j int) bool { return betterPeer(others[i], others[j]) })
		best := others[0]
		mean := 0.0
		for _, p := range others {
			mean += p.Q
		}
		mean /= float64(len(others))

		st := m.getRotationState(lv.Level, lv.Leader.ID, now)
		underperform := lv.Leader.Q < mean*(1-cfg.RotationDropPct)
		eligible := best.Q >= lv.Leader.Q+cfg.RotationMinDelta
		if !underperform || !eligible {
			m.clearRotationTrigger(lv.Level)
			continue
		}
		if st.TriggerSince.IsZero() {
			m.setRotationTrigger(lv.Level, now)
			continue
		}
		if now.Sub(st.TriggerSince) < window {
			continue
		}
		if !st.LeaderSince.IsZero() && now.Sub(st.LeaderSince) < minTenure {
			continue
		}
		if !st.LastRotation.IsZero() && cooldown > 0 && now.Sub(st.LastRotation) < cooldown {
			continue
		}

		if m.rotateLevelLeaderWithChoreography(lv, best, mean, cfg) {
			m.markRotationDone(lv.Level, best.ID, now)
			log.Printf(
				"comp=dashboard event=leader_rotate level=%d old=%d new=%d mean=%.4f q_old=%.4f q_new=%.4f",
				lv.Level, lv.Leader.ID, best.ID, mean, lv.Leader.Q, best.Q,
			)
			return // at most one rotation per tick
		}
	}
}

func (m *Manager) getRotationState(level int, leader node.NodeID, now time.Time) levelRotationState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.levelRotation[level]
	if st == nil {
		st = &levelRotationState{
			ObservedLeader: leader,
			LeaderSince:    now,
		}
		m.levelRotation[level] = st
	}
	if st.ObservedLeader != leader {
		st.ObservedLeader = leader
		st.LeaderSince = now
		st.TriggerSince = time.Time{}
	}
	return *st
}

func (m *Manager) setRotationTrigger(level int, ts time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.levelRotation[level]; st != nil && st.TriggerSince.IsZero() {
		st.TriggerSince = ts
	}
}

func (m *Manager) clearRotationTrigger(level int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.levelRotation[level]; st != nil {
		st.TriggerSince = time.Time{}
	}
}

func (m *Manager) resetRotationTrigger(level int, leader node.NodeID, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.levelRotation[level]
	if st == nil {
		st = &levelRotationState{}
		m.levelRotation[level] = st
	}
	st.ObservedLeader = leader
	st.LeaderSince = now
	st.TriggerSince = time.Time{}
}

func (m *Manager) markRotationDone(level int, newLeader node.NodeID, ts time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.levelRotation[level]
	if st == nil {
		st = &levelRotationState{}
		m.levelRotation[level] = st
	}
	st.ObservedLeader = newLeader
	st.LeaderSince = ts
	st.LastRotation = ts
	st.TriggerSince = time.Time{}
}

func validateStartConfig(cfg *StartConfig) error {
	if cfg.N <= 0 {
		return errors.New("n must be > 0")
	}
	if cfg.Y <= 0 {
		return errors.New("y must be > 0")
	}
	if cfg.Nodes < 0 {
		return errors.New("nodes must be >= 0")
	}
	if cfg.TickMS <= 0 {
		return errors.New("tick_ms must be > 0")
	}
	if cfg.InboxSize <= 0 {
		return errors.New("inbox_size must be > 0")
	}
	if cfg.SubnetSize == 0 {
		return errors.New("subnet_size must be > 0")
	}
	if cfg.SpawnParallelism < 0 {
		return errors.New("spawn_parallelism must be >= 0")
	}
	if cfg.SpawnDelayMS < 0 {
		return errors.New("spawn_delay_ms must be >= 0")
	}
	if cfg.ShutdownTimeoutMS < 0 {
		return errors.New("shutdown_timeout_ms must be >= 0")
	}
	if cfg.UDPIntervalMS <= 0 {
		return errors.New("udp_interval_ms must be > 0")
	}
	if cfg.UDPSubnets <= 0 {
		return errors.New("udp_subnets must be >= 1")
	}
	if cfg.UDPDelayMinMS < 0 || cfg.UDPDelayMaxMS < 0 {
		return errors.New("udp delays must be >= 0")
	}
	if cfg.UDPDelayMaxMS < cfg.UDPDelayMinMS {
		return errors.New("udp_delay_max_ms must be >= udp_delay_min_ms")
	}
	if cfg.UDPDropRate < 0 || cfg.UDPDropRate > 1 {
		return errors.New("udp_drop_rate must be in [0,1]")
	}
	if cfg.UDPSender != "root" && cfg.UDPSender != "random" {
		return errors.New("udp_sender must be root or random")
	}
	if cfg.RandomDiscoverFanout <= 0 {
		cfg.RandomDiscoverFanout = 4
	}
	if cfg.RandomDiscoverBackoffBaseMS <= 0 {
		cfg.RandomDiscoverBackoffBaseMS = 500
	}
	if cfg.RandomDiscoverBackoffMaxMS <= 0 {
		cfg.RandomDiscoverBackoffMaxMS = 8000
	}
	if cfg.RandomDiscoverBackoffMaxMS < cfg.RandomDiscoverBackoffBaseMS {
		return errors.New("random_discover_backoff_max_ms must be >= random_discover_backoff_base_ms")
	}
	if cfg.RandomDiscoverBackoffJitterMS < 0 {
		return errors.New("random_discover_backoff_jitter_ms must be >= 0")
	}
	if cfg.OfferRateLimitPerSec <= 0 {
		cfg.OfferRateLimitPerSec = 25
	}
	if cfg.OfferBurst <= 0 {
		cfg.OfferBurst = 50
	}
	if cfg.QDriftPctPerSec < 0 || cfg.QDriftPctPerSec > 1 {
		return errors.New("q_drift_pct_per_sec must be in [0,1]")
	}
	if cfg.QUpdateIntervalMS <= 0 {
		cfg.QUpdateIntervalMS = 1000
	}
	if cfg.QFreezeAfterMS < 0 {
		return errors.New("q_freeze_after_ms must be >= 0")
	}
	if cfg.MinReparentIntervalMS < 0 {
		return errors.New("min_reparent_interval_ms must be >= 0")
	}
	if cfg.UDPKind == "" {
		cfg.UDPKind = "DISCOVER"
	}
	if cfg.RotationDropPct < 0 || cfg.RotationDropPct >= 1 {
		return errors.New("rotation_drop_pct must be in [0,1)")
	}
	if cfg.RotationMinDelta < 0 || cfg.RotationMinDelta > 1 {
		return errors.New("rotation_min_delta must be in [0,1]")
	}
	if cfg.RotationWindowMS <= 0 {
		return errors.New("rotation_window_ms must be > 0")
	}
	if cfg.RotationMinTenureMS <= 0 {
		return errors.New("rotation_min_tenure_ms must be > 0")
	}
	if cfg.RotationCooldownMS < 0 {
		return errors.New("rotation_cooldown_ms must be >= 0")
	}
	if cfg.RotationCheckMS <= 0 {
		return errors.New("rotation_check_ms must be > 0")
	}
	if cfg.RotationPrepareQuorumPct <= 0 {
		cfg.RotationPrepareQuorumPct = 0.67
	}
	if cfg.RotationPrepareQuorumPct > 1 {
		return errors.New("rotation_prepare_quorum_pct must be in (0,1]")
	}
	if cfg.RotationPrepareTimeoutMS <= 0 {
		cfg.RotationPrepareTimeoutMS = 1200
	}
	if cfg.RotationCommitQuorumPct <= 0 {
		cfg.RotationCommitQuorumPct = 0.67
	}
	if cfg.RotationCommitQuorumPct > 1 {
		return errors.New("rotation_commit_quorum_pct must be in (0,1]")
	}
	if cfg.RotationCommitTimeoutMS <= 0 {
		cfg.RotationCommitTimeoutMS = 1200
	}
	return nil
}

func shouldRunLiveDiscoveryDuringSpawn(cfg StartConfig) bool {
	if !cfg.UDPDemo || cfg.UDPSender != "random" {
		return false
	}
	// Keep progressive discovery for small test scenarios.
	// Also enable it for delayed spawn to avoid a long no-traffic window when
	// nodes > 200 and spawn_delay_ms > 0.
	if cfg.SpawnDelayMS > 0 {
		return true
	}
	// For larger bootstrap sizes with no spawn delay, defer discovery until
	// spawn completes.
	return cfg.Nodes > 0 && cfg.Nodes <= 200
}

func spawnProgressEvery(total int) int {
	switch {
	case total <= 0:
		return 0
	case total <= 20:
		return 1
	case total <= 200:
		return 10
	case total <= 2000:
		return 50
	default:
		return 250
	}
}

func spawnModeName(async bool) string {
	if async {
		return "async"
	}
	return "sync"
}

func (m *Manager) spawnAndRegisterNodes(ctx context.Context, sim *mesh.Mesh, fabric *connsim.Fabric, wsFabric *connsim.WSFabric, cfg StartConfig) error {
	spawnStartedAt := time.Now()
	liveDiscovery := shouldRunLiveDiscoveryDuringSpawn(cfg)
	progressEvery := spawnProgressEvery(cfg.Nodes)
	var spawned atomic.Uint64

	registerNode := func(n *node.Node) {
		subnet := connsim.NodeSubnetForIndex(n.ID, cfg.UDPSubnets)
		if n.Manager != nil {
			n.Manager.SetConnectionState(node.ConnStateSolo)
		}
		n.SetOnMessage(m.buildNodeHandler(n.ID))
		fabric.RegisterNode(n, subnet)
		if wsFabric != nil {
			wsFabric.RegisterNode(n)
		}
		if freezeRemaining := m.qDriftFreezeRemaining(time.Now()); freezeRemaining > 0 {
			n.FreezeQDriftFor(freezeRemaining)
		}
		done := int(spawned.Add(1))
		m.mu.Lock()
		m.spawnedNodes = done
		m.spawnTotal = cfg.Nodes
		m.mu.Unlock()
		if progressEvery > 0 && (done == cfg.Nodes || done%progressEvery == 0) {
			elapsed := time.Since(spawnStartedAt)
			rate := float64(done)
			if elapsed > 0 {
				rate = rate / elapsed.Seconds()
			}
			pct := 100.0
			if cfg.Nodes > 0 {
				pct = (float64(done) / float64(cfg.Nodes)) * 100.0
			}
			log.Printf(
				"comp=dashboard event=spawn_progress spawned=%d total=%d pct=%.1f elapsed=%s rate=%.0f_nodes_s",
				done, cfg.Nodes, pct, elapsed.Round(time.Millisecond), rate,
			)
		}

		// Let freshly spawned nodes start discovery immediately in random mode,
		// without waiting for the next global UDP tick.
		if liveDiscovery {
			sent := m.sendRandomDiscoveryOnce(fabric, n.ID, cfg.UDPKind, []byte(cfg.UDPPayload), cfg.RandomDiscoverFanout)
			m.updateDiscoveryBackoff(n.ID, sent, time.Now(), cfg)
		}
	}

	waitDelay := func() error {
		if cfg.SpawnDelayMS <= 0 {
			return nil
		}
		timer := time.NewTimer(time.Duration(cfg.SpawnDelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}

	if !cfg.AsyncSpawn {
		for i := 0; i < cfg.Nodes; i++ {
			if err := waitDelay(); err != nil {
				return err
			}
			n, err := sim.AddNodeUnstarted(ctx)
			if err != nil {
				return err
			}
			registerNode(n)
			sim.StartNode(n)
		}
		return nil
	}

	parallelism := cfg.SpawnParallelism
	if parallelism <= 0 {
		parallelism = runtime.GOMAXPROCS(0)
	}
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	for i := 0; i < cfg.Nodes; i++ {
		if err := waitDelay(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			n, err := sim.AddNodeUnstarted(ctx)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			registerNode(n)
			sim.StartNode(n)
		}()
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		log.Printf(
			"comp=dashboard event=spawn_done total=%d elapsed=%s mode=%s live_discovery=%t",
			cfg.Nodes, time.Since(spawnStartedAt).Round(time.Millisecond), spawnModeName(cfg.AsyncSpawn), liveDiscovery,
		)
		return nil
	}
}

func (m *Manager) buildNodeHandler(self node.NodeID) func(*node.Node, node.Message) {
	return func(_ *node.Node, msg node.Message) {
		switch msg.Type {
		case node.MsgUDPDatagram:
			dg, ok := msg.Payload.(node.UDPDatagram)
			if !ok {
				return
			}
			switch dg.Kind {
			case "DISCOVER":
				if dg.From == self {
					return
				}
				m.addDiscoverRecv(1)
				mode := m.udpSenderMode()
				if mode == "root" && self != m.ensureRootNodeID() {
					return
				}
				if !m.canOffer() {
					return
				}
				if !m.allowOffer(self) {
					return
				}
				trace := dg.TraceID + "-offer"
				log.Printf("comp=dashboard event=offer_send from=%d to=%d trace=%s", self, dg.From, trace)
				m.mu.Lock()
				fabric := m.fabric
				m.mu.Unlock()
				if fabric != nil {
					if fabric.Send(self, dg.From, "OFFER", []byte("offer"), trace) {
						m.addOfferSent(1)
					}
				}
			case "OFFER":
				m.addOfferRecv(1)
				log.Printf("comp=dashboard event=offer_recv from=%d to=%d trace=%s", dg.From, self, dg.TraceID)
				m.startWSJoin(self, dg.From, dg.TraceID)
			}
		case node.MsgWSFrame:
			frame, ok := msg.Payload.(node.WSFrame)
			if !ok {
				return
			}
			m.handleWSFrame(self, frame)
		}
	}
}

func (m *Manager) canOffer() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *Manager) startWSJoin(from, target node.NodeID, trace string) {
	m.mu.Lock()
	if !m.running || m.m == nil || m.wsFabric == nil {
		m.mu.Unlock()
		return
	}
	fromNode := m.m.NodeByID(from)
	targetNode := m.m.NodeByID(target)
	wsFabric := m.wsFabric
	cfg := m.cfg
	m.mu.Unlock()
	if fromNode == nil || targetNode == nil || wsFabric == nil {
		return
	}

	if fromNode.Manager != nil {
		state := fromNode.Manager.Snapshot().ConnectionState
		if state != node.ConnStateSolo && state != node.ConnStateDisconnected {
			return
		}
		fromNode.Manager.SetConnectionState(node.ConnStateJoining)
	}
	m.markJoinAttempt(from)

	if _, ok := wsFabric.Connect(from, target, trace+"-ws"); !ok {
		if fromNode.Manager != nil {
			fromNode.Manager.SetConnectionState(node.ConnStateSolo)
		}
		m.markJoinFail(from)
		return
	}

	senderThreshold := 0.7
	if fromNode.Manager != nil {
		senderThreshold = fromNode.Manager.Config().SenderThreshold
	}
	hello := node.NewWSHelloPacket(trace+"-hello", 0, fromNode.PeerInfo(), senderThreshold)
	if cfg.Seed != 0 {
		hello.Epoch = uint64(cfg.Seed)
	}
	payload, err := node.EncodeWSPacket(hello)
	if err != nil {
		log.Printf("comp=dashboard event=hello_build_err from=%d to=%d err=%v", from, target, err)
		if fromNode.Manager != nil {
			fromNode.Manager.SetConnectionState(node.ConnStateSolo)
		}
		m.markJoinFail(from)
		return
	}
	if !wsFabric.SendFrame(from, target, string(node.WSControlHello), payload, trace+"-hello") {
		log.Printf("comp=dashboard event=hello_send_err from=%d to=%d trace=%s", from, target, trace)
		if fromNode.Manager != nil {
			fromNode.Manager.SetConnectionState(node.ConnStateSolo)
		}
		m.markJoinFail(from)
		return
	}
	m.addWSSentByType(node.WSControlHello)
	log.Printf("comp=dashboard event=hello_send from=%d to=%d trace=%s", from, target, trace)
}

func (m *Manager) handleWSFrame(self node.NodeID, frame node.WSFrame) {
	t, err := node.DecodeWSPacketType(frame.Payload)
	if err != nil {
		log.Printf("comp=dashboard event=ws_decode_type_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
		return
	}
	m.addWSRecvByType(t)
	switch t {
	case node.WSControlHello:
		pkt, err := node.DecodeWSHelloPacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=hello_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSHello(self, frame, pkt)
	case node.WSControlHelloRelay:
		pkt, err := node.DecodeWSHelloRelayPacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=hello_relay_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSHelloRelay(self, frame, pkt)
	case node.WSControlHelloAck:
		pkt, err := node.DecodeWSHelloAckPacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=hello_ack_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSHelloAck(self, frame, pkt)
	case node.WSControlTransitionHint:
		pkt, err := node.DecodeWSTransitionHintPacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=transition_hint_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSTransitionHint(self, frame, pkt)
	case node.WSControlRotatePrepare:
		pkt, err := node.DecodeWSLeaderRotatePreparePacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=rotate_prepare_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSRotatePrepare(self, frame, pkt)
	case node.WSControlRotatePrepareAck:
		pkt, err := node.DecodeWSLeaderRotatePrepareAckPacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=rotate_prepare_ack_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSRotatePrepareAck(self, frame, pkt)
	case node.WSControlRotateCommit:
		pkt, err := node.DecodeWSLeaderRotateCommitPacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=rotate_commit_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSRotateCommit(self, frame, pkt)
	case node.WSControlRotateCommitAck:
		pkt, err := node.DecodeWSLeaderRotateCommitAckPacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=rotate_commit_ack_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSRotateCommitAck(self, frame, pkt)
	case node.WSControlRotateApplied:
		pkt, err := node.DecodeWSLeaderRotateAppliedPacket(frame.Payload)
		if err != nil {
			log.Printf("comp=dashboard event=rotate_applied_decode_err to=%d from=%d err=%v trace=%s", self, frame.From, err, frame.TraceID)
			return
		}
		m.onWSRotateApplied(self, frame, pkt)
	default:
		log.Printf("comp=dashboard event=ws_ignored to=%d from=%d type=%s trace=%s", self, frame.From, t, frame.TraceID)
	}
}

func (m *Manager) onWSHello(self node.NodeID, frame node.WSFrame, hello node.WSHelloPacket) {
	if target, direction, relay := m.chooseRelayTarget(self, hello.From.ID, hello.From.Q); relay {
		m.forwardHelloRelay(self, target, frame.TraceID, hello.From, 0, direction)
		return
	}
	m.finalizeJoinPlacement(hello.From.ID, self, 0, frame.TraceID, hello.SenderThreshold)
}

func (m *Manager) onWSHelloRelay(self node.NodeID, frame node.WSFrame, relay node.WSHelloRelayPacket) {
	if relay.Hop >= relay.MaxHop {
		m.finalizeJoinPlacement(relay.Origin.ID, self, 0, frame.TraceID, 0.7)
		return
	}
	if target, direction, shouldRelay := m.chooseRelayTarget(self, relay.Origin.ID, relay.Origin.Q); shouldRelay {
		m.forwardHelloRelay(self, target, frame.TraceID, relay.Origin, relay.Hop+1, direction)
		return
	}
	m.finalizeJoinPlacement(relay.Origin.ID, self, 0, frame.TraceID, 0.7)
}

func (m *Manager) onWSHelloAck(self node.NodeID, frame node.WSFrame, ack node.WSHelloAckPacket) {
	m.mu.Lock()
	if !m.running || m.m == nil {
		m.mu.Unlock()
		return
	}
	selfNode := m.m.NodeByID(self)
	m.mu.Unlock()
	if selfNode == nil || selfNode.Manager == nil {
		return
	}
	current := selfNode.Manager.Snapshot()
	if current.TopologyRevision > 0 && ack.TopologyRevision > 0 && ack.TopologyRevision < current.TopologyRevision {
		log.Printf(
			"comp=dashboard event=hello_ack_stale_drop node=%d ack_rev=%d current_rev=%d trace=%s",
			self, ack.TopologyRevision, current.TopologyRevision, frame.TraceID,
		)
		return
	}
	m.markJoinSuccess(self)

	selfNode.SetRole(ack.AssignedRole)
	selfNode.SetLevel(ack.AssignedLevel)
	totalLevels := ack.TotalLevels
	if totalLevels <= 0 {
		totalLevels = 1
	}
	selfNode.Manager.SetHierarchyPosition(ack.AssignedLevel, totalLevels, ack.TopologyRevision)
	if ack.Leader.ID != 0 && ack.Leader.ID != self {
		selfNode.Manager.SetCurrentLeader(ack.Leader)
		selfNode.Manager.UpsertActiveConnection(ack.Leader)
		selfNode.Manager.SetConnectionState(node.ConnStateConnected)
		m.tryConnect(self, ack.Leader.ID, frame.TraceID+"-ack")
	} else {
		selfNode.Manager.ClearCurrentLeader()
		selfNode.Manager.SetConnectionState(node.ConnStateConnected)
	}
	selfNode.Manager.SetSameLevel(ack.SameLevel)
	selfNode.Manager.SetUpperLevels(ack.UpperLevels)
	log.Printf(
		"comp=dashboard event=hello_ack_apply node=%d leader=%d role=%s level=%d total_levels=%d topo_rev=%d trace=%s",
		self, ack.Leader.ID, ack.AssignedRole.String(), ack.AssignedLevel, totalLevels, ack.TopologyRevision, frame.TraceID,
	)
}

func (m *Manager) onWSTransitionHint(self node.NodeID, frame node.WSFrame, hint node.WSTransitionHintPacket) {
	m.mu.Lock()
	if !m.running || m.m == nil {
		m.mu.Unlock()
		return
	}
	cfg := m.cfg
	sim := m.m
	selfNode := sim.NodeByID(self)
	targetNode := sim.NodeByID(hint.NewTarget.ID)
	m.mu.Unlock()
	if selfNode == nil || selfNode.Manager == nil {
		return
	}

	if hint.Node.ID != 0 && hint.Node.ID != self {
		selfNode.Manager.ClearCurrentLeader()
		selfNode.Manager.ClearActiveConnections()
		selfNode.Manager.SetConnectionState(node.ConnStateDisconnected)
		log.Printf(
			"comp=dashboard event=transition_hint_drop node=%d payload_node=%d from=%d reason=node_mismatch trace=%s",
			self, hint.Node.ID, frame.From, frame.TraceID,
		)
		return
	}

	prev := selfNode.Manager.Snapshot()
	if prev.CurrentLeader.ID == hint.NewTarget.ID && hint.NewTarget.ID != 0 && len(prev.ActiveConnections) > 0 {
		selfNode.Manager.SetConnectionState(node.ConnStateConnected)
		return
	}
	selfNode.Manager.SetConnectionState(node.ConnStateTransitioning)
	if targetNode == nil || hint.NewTarget.ID == 0 || hint.NewTarget.ID == self {
		selfNode.Manager.ClearCurrentLeader()
		selfNode.Manager.ClearActiveConnections()
		selfNode.Manager.SetConnectionState(node.ConnStateDisconnected)
		log.Printf(
			"comp=dashboard event=transition_hint_invalid_target node=%d target=%d from=%d trace=%s",
			self, hint.NewTarget.ID, frame.From, frame.TraceID,
		)
		return
	}
	wasConnected := prev.CurrentLeader.ID != 0 && len(prev.ActiveConnections) > 0
	if throttle, wait := m.shouldThrottleReparent(self, prev.CurrentLeader.ID, targetNode.ID, wasConnected, cfg, time.Now()); throttle {
		if prev.CurrentLeader.ID != 0 {
			selfNode.Manager.SetCurrentLeader(prev.CurrentLeader)
			selfNode.Manager.ClearActiveConnections()
			selfNode.Manager.UpsertActiveConnection(prev.CurrentLeader)
			selfNode.Manager.SetConnectionState(node.ConnStateConnected)
		} else {
			selfNode.Manager.ClearCurrentLeader()
			selfNode.Manager.ClearActiveConnections()
			selfNode.Manager.SetConnectionState(node.ConnStateDisconnected)
		}
		log.Printf(
			"comp=dashboard event=transition_hint_throttled node=%d old_leader=%d new_target=%d wait_ms=%d trace=%s",
			self, prev.CurrentLeader.ID, targetNode.ID, wait.Milliseconds(), frame.TraceID,
		)
		return
	}

	selfNode.Manager.SetCurrentLeader(targetNode.PeerInfo())
	selfNode.Manager.ClearActiveConnections()
	selfNode.Manager.UpsertActiveConnection(targetNode.PeerInfo())
	m.tryConnect(self, targetNode.ID, frame.TraceID+"-hint")

	post := selfNode.Manager.Snapshot()
	if post.CurrentLeader.ID != 0 && len(post.ActiveConnections) > 0 {
		selfNode.Manager.SetConnectionState(node.ConnStateConnected)
	} else {
		selfNode.Manager.SetConnectionState(node.ConnStateDisconnected)
	}
	log.Printf(
		"comp=dashboard event=transition_hint_apply node=%d old_from=%d new_target=%d level=%d keep_old=%t trace=%s",
		self, frame.From, targetNode.ID, selfNode.Level, hint.KeepOldUntilAck, frame.TraceID,
	)
}

func (m *Manager) onWSRotatePrepare(self node.NodeID, frame node.WSFrame, pkt node.WSLeaderRotatePreparePacket) {
	m.mu.Lock()
	if !m.running || m.m == nil {
		m.mu.Unlock()
		return
	}
	selfNode := m.m.NodeByID(self)
	m.mu.Unlock()
	if selfNode == nil || selfNode.Manager == nil {
		return
	}

	snap := selfNode.Manager.Snapshot()
	if snap.TopologyRevision > 0 && pkt.CurrentRevision > 0 && pkt.CurrentRevision < snap.TopologyRevision {
		m.sendRotatePrepareAck(selfNode, frame.From, pkt, false, "stale_revision", frame.TraceID+"-ack")
		log.Printf(
			"comp=dashboard event=rotate_prepare_stale_drop node=%d level=%d pkt_rev=%d current_rev=%d trace=%s",
			self, pkt.Level, pkt.CurrentRevision, snap.TopologyRevision, frame.TraceID,
		)
		return
	}
	if selfNode.Level == pkt.Level || self == pkt.OldLeader.ID || self == pkt.NewLeader.ID {
		selfNode.Manager.SetConnectionState(node.ConnStateTransitioning)
	}
	log.Printf(
		"comp=dashboard event=rotate_prepare_recv node=%d level=%d old=%d new=%d current_rev=%d mean=%.4f q_old=%.4f q_new=%.4f trace=%s",
		self, pkt.Level, pkt.OldLeader.ID, pkt.NewLeader.ID, pkt.CurrentRevision, pkt.MeanQ, pkt.OldLeaderQ, pkt.CandidateQ, frame.TraceID,
	)
	m.sendRotatePrepareAck(selfNode, frame.From, pkt, true, "", frame.TraceID+"-ack")
}

func (m *Manager) onWSRotatePrepareAck(self node.NodeID, frame node.WSFrame, pkt node.WSLeaderRotatePrepareAckPacket) {
	_ = self
	fromID := frame.From
	if pkt.ProposalID == "" || fromID == 0 {
		return
	}
	if pkt.FromPeer.ID != 0 && pkt.FromPeer.ID != fromID {
		log.Printf(
			"comp=dashboard event=rotate_prepare_ack_drop proposal=%s frame_from=%d payload_from=%d reason=from_mismatch trace=%s",
			pkt.ProposalID, fromID, pkt.FromPeer.ID, frame.TraceID,
		)
		return
	}
	proposal := m.getRotationPrepareProposal(pkt.ProposalID)
	if proposal == nil {
		log.Printf(
			"comp=dashboard event=rotate_prepare_ack_ignored proposal=%s from=%d reason=no_pending trace=%s",
			pkt.ProposalID, fromID, frame.TraceID,
		)
		return
	}
	accepted, rejected, reached := proposal.addAck(fromID, pkt.Accept, pkt.Reason)
	log.Printf(
		"comp=dashboard event=rotate_prepare_ack_recv proposal=%s from=%d accept=%t accepted=%d rejected=%d quorum=%d reached=%t trace=%s",
		pkt.ProposalID, fromID, pkt.Accept, accepted, rejected, proposal.quorum, reached, frame.TraceID,
	)
}

func (m *Manager) onWSRotateCommit(self node.NodeID, frame node.WSFrame, pkt node.WSLeaderRotateCommitPacket) {
	m.mu.Lock()
	if !m.running || m.m == nil {
		m.mu.Unlock()
		return
	}
	selfNode := m.m.NodeByID(self)
	m.mu.Unlock()
	if selfNode == nil || selfNode.Manager == nil {
		return
	}

	snap := selfNode.Manager.Snapshot()
	if snap.TopologyRevision > 0 && pkt.TargetRevision > 0 && pkt.TargetRevision < snap.TopologyRevision {
		m.sendRotateCommitAck(selfNode, frame.From, pkt, false, "stale_revision", frame.TraceID+"-ack")
		log.Printf(
			"comp=dashboard event=rotate_commit_stale_drop node=%d level=%d target_rev=%d current_rev=%d trace=%s",
			self, pkt.Level, pkt.TargetRevision, snap.TopologyRevision, frame.TraceID,
		)
		return
	}
	if selfNode.Level == pkt.Level || self == pkt.OldLeader.ID || self == pkt.NewLeader.ID {
		selfNode.Manager.SetConnectionState(node.ConnStateTransitioning)
	}
	log.Printf(
		"comp=dashboard event=rotate_commit_recv node=%d level=%d old=%d new=%d prev_rev=%d target_rev=%d trace=%s",
		self, pkt.Level, pkt.OldLeader.ID, pkt.NewLeader.ID, pkt.PrevRevision, pkt.TargetRevision, frame.TraceID,
	)
	m.sendRotateCommitAck(selfNode, frame.From, pkt, true, "", frame.TraceID+"-ack")
}

func (m *Manager) onWSRotateCommitAck(self node.NodeID, frame node.WSFrame, pkt node.WSLeaderRotateCommitAckPacket) {
	_ = self
	fromID := frame.From
	if pkt.ProposalID == "" || fromID == 0 {
		return
	}
	if pkt.FromPeer.ID != 0 && pkt.FromPeer.ID != fromID {
		log.Printf(
			"comp=dashboard event=rotate_commit_ack_drop proposal=%s frame_from=%d payload_from=%d reason=from_mismatch trace=%s",
			pkt.ProposalID, fromID, pkt.FromPeer.ID, frame.TraceID,
		)
		return
	}
	proposal := m.getRotationCommitProposal(pkt.ProposalID)
	if proposal == nil {
		log.Printf(
			"comp=dashboard event=rotate_commit_ack_ignored proposal=%s from=%d reason=no_pending trace=%s",
			pkt.ProposalID, fromID, frame.TraceID,
		)
		return
	}
	accepted, rejected, reached := proposal.addAck(fromID, pkt.Accept, pkt.Reason)
	log.Printf(
		"comp=dashboard event=rotate_commit_ack_recv proposal=%s from=%d accept=%t accepted=%d rejected=%d quorum=%d reached=%t trace=%s",
		pkt.ProposalID, fromID, pkt.Accept, accepted, rejected, proposal.quorum, reached, frame.TraceID,
	)
}

func (m *Manager) onWSRotateApplied(self node.NodeID, frame node.WSFrame, pkt node.WSLeaderRotateAppliedPacket) {
	m.mu.Lock()
	if !m.running || m.m == nil {
		m.mu.Unlock()
		return
	}
	selfNode := m.m.NodeByID(self)
	m.mu.Unlock()
	if selfNode == nil || selfNode.Manager == nil {
		return
	}

	snap := selfNode.Manager.Snapshot()
	if snap.TopologyRevision > 0 && pkt.FinalRevision > 0 && pkt.FinalRevision < snap.TopologyRevision {
		log.Printf(
			"comp=dashboard event=rotate_applied_stale_drop node=%d level=%d final_rev=%d current_rev=%d trace=%s",
			self, pkt.Level, pkt.FinalRevision, snap.TopologyRevision, frame.TraceID,
		)
		return
	}
	totalLevels := snap.TotalLevelsKnown
	if totalLevels <= 0 {
		totalLevels = 1
	}
	selfNode.Manager.SetHierarchyPosition(selfNode.Level, totalLevels, pkt.FinalRevision)
	selfNode.Manager.SetConnectionState(node.ConnStateConnected)
	log.Printf(
		"comp=dashboard event=rotate_applied_recv node=%d level=%d old=%d new=%d final_rev=%d applied_by=%d trace=%s",
		self, pkt.Level, pkt.OldLeader.ID, pkt.NewLeader.ID, pkt.FinalRevision, pkt.AppliedBy.ID, frame.TraceID,
	)
}

func (m *Manager) chooseRelayTarget(selfID, originID node.NodeID, originQ float64) (node.NodeID, string, bool) {
	m.mu.Lock()
	sim := m.m
	cfg := m.cfg
	parentByNode := make(map[node.NodeID]node.NodeID, len(m.parentByNode))
	for k, v := range m.parentByNode {
		parentByNode[k] = v
	}
	childLeaders := make(map[node.NodeID][]node.NodeID, len(m.childLeadersByNode))
	for k, vv := range m.childLeadersByNode {
		childLeaders[k] = append([]node.NodeID(nil), vv...)
	}
	followers := make(map[node.NodeID][]node.NodeID, len(m.followersByLeader))
	for k, vv := range m.followersByLeader {
		followers[k] = append([]node.NodeID(nil), vv...)
	}
	m.mu.Unlock()
	if sim == nil {
		return 0, "", false
	}
	selfNode := sim.NodeByID(selfID)
	if selfNode == nil {
		return 0, "", false
	}

	if selfNode.Role != node.RoleLeader {
		if parentID := parentByNode[selfID]; parentID != 0 && parentID != originID {
			return parentID, "up", true
		}
		return 0, "", false
	}

	// Upward relay: stronger-than-current leader should bubble up to parent leader.
	if parentID := parentByNode[selfID]; parentID != 0 && originQ > selfNode.Metrics.Q && parentID != originID {
		return parentID, "up", true
	}

	capacity := cfg.N + 1
	if capacity <= 1 {
		capacity = 2
	}
	memberIDs := make([]node.NodeID, 0, 1+len(followers[selfID]))
	memberIDs = append(memberIDs, selfID)
	memberIDs = append(memberIDs, followers[selfID]...)
	if len(memberIDs) >= capacity {
		weakestQ := selfNode.Metrics.Q
		for _, id := range memberIDs {
			if n := sim.NodeByID(id); n != nil && n.Metrics.Q < weakestQ {
				weakestQ = n.Metrics.Q
			}
		}
		if originQ < weakestQ {
			for _, childID := range childLeaders[selfID] {
				if childID != 0 && childID != selfID && childID != originID {
					return childID, "down", true
				}
			}
		}
	}

	return 0, "", false
}

func (m *Manager) forwardHelloRelay(from, to node.NodeID, trace string, origin node.PeerInfo, hop int, direction string) {
	m.mu.Lock()
	if !m.running || m.m == nil || m.wsFabric == nil {
		m.mu.Unlock()
		return
	}
	fromNode := m.m.NodeByID(from)
	wsFabric := m.wsFabric
	m.mu.Unlock()
	if fromNode == nil || wsFabric == nil {
		return
	}

	if _, ok := wsFabric.Connect(from, to, trace+"-relay-link"); !ok {
		return
	}
	relay := node.NewWSHelloRelayPacket(trace+"-relay", 0, origin, fromNode.PeerInfo(), direction)
	relay.Hop = hop
	payload, err := node.EncodeWSPacket(relay)
	if err != nil {
		log.Printf("comp=dashboard event=hello_relay_build_err from=%d to=%d err=%v trace=%s", from, to, err, trace)
		return
	}
	if wsFabric.SendFrame(from, to, string(node.WSControlHelloRelay), payload, trace+"-relay") {
		m.addWSSentByType(node.WSControlHelloRelay)
		log.Printf("comp=dashboard event=hello_relay_send from=%d to=%d origin=%d hop=%d direction=%s trace=%s", from, to, origin.ID, hop, direction, trace)
	}
}

func (m *Manager) finalizeJoinPlacement(originID, acceptedByID, leaderID node.NodeID, trace string, senderThreshold float64) {
	_ = leaderID // leader selection is recomputed by global N/Y distribution.
	hs := m.applyPlacementCascade(originID, acceptedByID, senderThreshold)

	m.mu.Lock()
	if !m.running || m.m == nil || m.wsFabric == nil {
		m.mu.Unlock()
		return
	}
	sim := m.m
	wsFabric := m.wsFabric
	udpFabric := m.fabric
	originNode := sim.NodeByID(originID)
	acceptedByNode := sim.NodeByID(acceptedByID)
	n := m.cfg.UDPSubnets
	m.mu.Unlock()
	if originNode == nil || acceptedByNode == nil {
		return
	}

	originSnap := node.NodeManagerSnapshot{}
	if originNode.Manager != nil {
		originSnap = originNode.Manager.Snapshot()
	}
	m.mu.Lock()
	parentID := m.parentByNode[originID]
	m.mu.Unlock()
	assignedLeader := originSnap.CurrentLeader
	if parentID != 0 {
		if p := sim.NodeByID(parentID); p != nil {
			assignedLeader = p.PeerInfo()
		}
	} else if originNode.Role == node.RoleLeader {
		assignedLeader = originNode.PeerInfo()
	}
	if assignedLeader.ID == 0 {
		assignedLeader = originNode.PeerInfo()
	}

	ackSenderID := assignedLeader.ID
	if ackSenderID == 0 {
		ackSenderID = acceptedByID
	}
	if sim.NodeByID(ackSenderID) == nil {
		ackSenderID = acceptedByID
	}

	if n > 0 && udpFabric != nil && originID != assignedLeader.ID && assignedLeader.ID != 0 {
		// Keep visualization of final logical WS attachment on topology view.
		_ = udpFabric.Send(originID, assignedLeader.ID, "CONNECT", []byte("connect"), trace+"-final")
	}
	if assignedLeader.ID != 0 && originID != assignedLeader.ID {
		m.tryConnect(originID, assignedLeader.ID, trace+"-connect")
	}

	ack := node.NewWSHelloAckPacket(trace+"-ack", 0, assignedLeader)
	ack.AcceptedBy = acceptedByNode.PeerInfo()
	ack.AssignedRole = originNode.Role
	ack.AssignedLevel = originNode.Level
	ack.TotalLevels = originSnap.TotalLevelsKnown
	if ack.TotalLevels <= 0 {
		ack.TotalLevels = hs.TotalLevels
		if ack.TotalLevels <= 0 {
			ack.TotalLevels = 1
		}
	}
	ack.TopologyRevision = originSnap.TopologyRevision
	if ack.TopologyRevision == 0 {
		ack.TopologyRevision = hs.Revision
	}
	ack.SameLevel = originSnap.SameLevel
	ack.UpperLevels = originSnap.UpperLevels
	ack.Failover = originSnap.Failover

	if _, ok := wsFabric.Connect(ackSenderID, originID, trace+"-ack-link"); !ok {
		return
	}
	payload, err := node.EncodeWSPacket(ack)
	if err != nil {
		log.Printf("comp=dashboard event=hello_ack_build_err origin=%d leader=%d err=%v trace=%s", originNode.ID, ackSenderID, err, trace)
		return
	}
	if wsFabric.SendFrame(ackSenderID, originID, string(node.WSControlHelloAck), payload, trace+"-ack") {
		m.addWSSentByType(node.WSControlHelloAck)
		log.Printf(
			"comp=dashboard event=hello_ack_send origin=%d leader=%d accepted_by=%d level=%d total_levels=%d topo_rev=%d trace=%s",
			originNode.ID, assignedLeader.ID, acceptedByNode.ID, ack.AssignedLevel, ack.TotalLevels, ack.TopologyRevision, trace,
		)
	}
}

func (m *Manager) applyPlacementCascade(originID, acceptedByID node.NodeID, senderThreshold float64) hierarchy.Snapshot {
	m.placementMu.Lock()
	defer m.placementMu.Unlock()

	_ = originID
	_ = acceptedByID

	if senderThreshold <= 0 || senderThreshold > 1 {
		senderThreshold = 0.7
	}

	m.mu.Lock()
	sim := m.m
	hier := m.hier
	cfg := m.cfg
	m.mu.Unlock()
	if sim == nil || hier == nil {
		return hierarchy.Snapshot{}
	}

	nodes := sim.NodesSnapshot()
	if len(nodes) == 0 {
		return hierarchy.Snapshot{}
	}

	filtered := make([]*node.Node, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		filtered = append(filtered, n)
	}
	if len(filtered) == 0 {
		return hierarchy.Snapshot{}
	}
	sortNodesByQuality(filtered)

	if cfg.Y <= 0 {
		cfg.Y = 1
	}
	if cfg.N < 0 {
		cfg.N = 0
	}

	type leaderQueueItem struct {
		leader *node.Node
		topoID string
		depth  int
	}

	topologyByNode := make(map[node.NodeID]string, len(filtered))
	parentByNode := make(map[node.NodeID]node.NodeID, len(filtered))
	childLeadersByNode := make(map[node.NodeID][]node.NodeID, len(filtered))
	followersByLeader := make(map[node.NodeID][]node.NodeID, len(filtered))

	assignLeader := func(n *node.Node, depth int, topoID string, parentID node.NodeID) {
		n.SetRole(node.RoleLeader)
		n.SetLevel(depth)
		topologyByNode[n.ID] = topoID
		if parentID != 0 {
			parentByNode[n.ID] = parentID
		} else {
			delete(parentByNode, n.ID)
		}
	}
	assignFollower := func(n *node.Node, depth int, topoID string, parentID node.NodeID) {
		if n.Metrics.Q >= senderThreshold {
			n.SetRole(node.RoleSender)
		} else {
			n.SetRole(node.RoleBase)
		}
		n.SetLevel(depth)
		topologyByNode[n.ID] = topoID
		if parentID != 0 {
			parentByNode[n.ID] = parentID
		} else {
			delete(parentByNode, n.ID)
		}
	}

	root := filtered[0]
	assignLeader(root, 0, "0", 0)
	queue := []leaderQueueItem{{leader: root, topoID: "0", depth: 0}}
	idx := 1
	for qi := 0; qi < len(queue) && idx < len(filtered); qi++ {
		cur := queue[qi]
		parentID := cur.leader.ID

		// Assign child leaders first (up to Y) so the backbone keeps higher-Q nodes.
		for slot := 1; slot <= cfg.Y && idx < len(filtered); slot++ {
			child := filtered[idx]
			idx++
			childTopo := nextLeaderTopologyID(cur.topoID, slot)
			assignLeader(child, cur.depth+1, childTopo, parentID)
			childLeadersByNode[parentID] = append(childLeadersByNode[parentID], child.ID)
			queue = append(queue, leaderQueueItem{leader: child, topoID: childTopo, depth: cur.depth + 1})
		}

		// Assign same-group followers for this leader (up to N).
		for slot := 1; slot <= cfg.N && idx < len(filtered); slot++ {
			follower := filtered[idx]
			idx++
			followerTopo := nextFollowerTopologyID(cur.topoID, slot)
			assignFollower(follower, cur.depth, followerTopo, parentID)
			followersByLeader[parentID] = append(followersByLeader[parentID], follower.ID)
		}
	}
	// Defensive fallback (should not happen with Y>=1): attach leftovers to root.
	for idx < len(filtered) {
		slot := len(followersByLeader[root.ID]) + 1
		follower := filtered[idx]
		idx++
		followerTopo := nextFollowerTopologyID("0", slot)
		assignFollower(follower, 0, followerTopo, root.ID)
		followersByLeader[root.ID] = append(followersByLeader[root.ID], follower.ID)
	}

	m.mu.Lock()
	m.topologyByNode = topologyByNode
	m.parentByNode = parentByNode
	m.childLeadersByNode = childLeadersByNode
	m.followersByLeader = followersByLeader
	m.mu.Unlock()

	updated := make([]node.PeerInfo, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		updated = append(updated, n.PeerInfo())
	}
	hs := hier.RebuildFromPeers(updated)
	m.syncManagersFromHierarchy(hs, cfg.Y)
	return hs
}

func (m *Manager) syncManagersFromHierarchy(hs hierarchy.Snapshot, y int) {
	m.mu.Lock()
	sim := m.m
	cfg := m.cfg
	parentByNode := make(map[node.NodeID]node.NodeID, len(m.parentByNode))
	for k, v := range m.parentByNode {
		parentByNode[k] = v
	}
	topoByNode := make(map[node.NodeID]string, len(m.topologyByNode))
	for k, v := range m.topologyByNode {
		topoByNode[k] = v
	}
	m.mu.Unlock()
	if sim == nil {
		return
	}
	nodes := sim.NodesSnapshot()
	totalLevels := hs.TotalLevels
	if totalLevels <= 0 {
		totalLevels = 1
	}
	levelMap := make(map[int]hierarchy.LevelState, len(hs.Levels))
	for _, st := range hs.Levels {
		levelMap[st.Level] = st
	}

	type transitionEvent struct {
		NodeID      node.NodeID
		OldLeaderID node.NodeID
		NewLeaderID node.NodeID
		OldLevel    int
		NewLevel    int
	}

	connected := make(map[node.NodeID]node.NodeID, len(nodes))
	transitions := make([]transitionEvent, 0)
	now := time.Now()
	for _, n := range nodes {
		if n == nil || n.Manager == nil {
			continue
		}
		prev := n.Manager.Snapshot()
		newLevel := n.Level
		st, ok := levelMap[newLevel]

		n.Manager.SetHierarchyPosition(newLevel, totalLevels, hs.Revision)
		if ok {
			n.Manager.SetSameLevel(st.Candidates)
		} else {
			n.Manager.SetSameLevel(nil)
		}

		upper := make([]node.UpperLevelView, 0, y)
		for upLevel := newLevel - 1; upLevel >= 0 && len(upper) < y; upLevel-- {
			if upState, exists := levelMap[upLevel]; exists {
				upper = append(upper, node.UpperLevelView{
					Level:      upState.Level,
					Leader:     upState.Leader,
					Candidates: upState.Candidates,
				})
			}
		}
		n.Manager.SetUpperLevels(upper)
		n.Manager.ClearActiveConnections()

		targetLeader := node.PeerInfo{}
		parentID := parentByNode[n.ID]
		if parentID != 0 {
			if parentNode := sim.NodeByID(parentID); parentNode != nil {
				if parentNode.ID != n.ID {
					targetLeader = parentNode.PeerInfo()
				}
			}
		}
		if targetLeader.ID == 0 && n.Role == node.RoleLeader && newLevel > 0 {
			if upperLeader, found := findNearestUpperLeader(newLevel, levelMap); found && upperLeader.ID != n.ID {
				targetLeader = upperLeader
			}
		}
		desiredLeader := targetLeader
		wasConnected := prev.CurrentLeader.ID != 0 && len(prev.ActiveConnections) > 0
		if throttle, wait := m.shouldThrottleReparent(n.ID, prev.CurrentLeader.ID, desiredLeader.ID, wasConnected, cfg, now); throttle {
			targetLeader = prev.CurrentLeader
			log.Printf(
				"comp=dashboard event=reparent_throttled node=%d old_leader=%d new_leader=%d wait_ms=%d",
				n.ID, prev.CurrentLeader.ID, desiredLeader.ID, wait.Milliseconds(),
			)
		}

		if targetLeader.ID != 0 {
			n.Manager.SetCurrentLeader(targetLeader)
			n.Manager.UpsertActiveConnection(targetLeader)
			connected[n.ID] = targetLeader.ID
		} else {
			n.Manager.ClearCurrentLeader()
		}

		levelChanged := prev.CurrentLevel != newLevel
		leaderChanged := prev.CurrentLeader.ID != targetLeader.ID
		moved := levelChanged || leaderChanged
		if moved && leaderChanged {
			transitions = append(transitions, transitionEvent{
				NodeID:      n.ID,
				OldLeaderID: prev.CurrentLeader.ID,
				NewLeaderID: targetLeader.ID,
				OldLevel:    prev.CurrentLevel,
				NewLevel:    newLevel,
			})
		}

		isRootLeader := topoByNode[n.ID] == "0" && n.Role == node.RoleLeader
		if moved && leaderChanged && targetLeader.ID != 0 {
			n.Manager.SetConnectionState(node.ConnStateTransitioning)
			continue
		}
		post := n.Manager.Snapshot()
		if isRootLeader {
			n.Manager.SetConnectionState(node.ConnStateConnected)
		} else if post.CurrentLeader.ID != 0 && len(post.ActiveConnections) > 0 {
			n.Manager.SetConnectionState(node.ConnStateConnected)
		} else {
			n.Manager.SetConnectionState(node.ConnStateDisconnected)
		}
	}

	m.mu.Lock()
	if m.connected == nil {
		m.connected = make(map[node.NodeID]node.NodeID, len(connected))
	}
	m.connected = connected
	m.mu.Unlock()

	for _, ev := range transitions {
		if ev.NewLeaderID == 0 || ev.NewLeaderID == ev.NodeID {
			continue
		}
		targetNode := sim.NodeByID(ev.NodeID)
		leaderNode := sim.NodeByID(ev.NewLeaderID)
		if targetNode == nil || leaderNode == nil || targetNode.Manager == nil {
			continue
		}
		trace := connsim.NewTraceID("reparent", m.traceSeq.Add(1))
		hint := node.NewWSTransitionHintPacket(trace+"-pkt", 0, targetNode.PeerInfo(), leaderNode.PeerInfo())
		hint.KeepOldUntilAck = true
		hint.Reason = "sync_reparent"
		_ = m.sendWSControl(leaderNode.ID, targetNode.ID, string(node.WSControlTransitionHint), hint, trace+"-hint")
		m.tryConnect(targetNode.ID, leaderNode.ID, trace+"-reparent")
		log.Printf(
			"comp=dashboard event=transition_emit node=%d old_leader=%d new_leader=%d old_level=%d new_level=%d trace=%s",
			ev.NodeID, ev.OldLeaderID, ev.NewLeaderID, ev.OldLevel, ev.NewLevel, trace,
		)
	}
}

func findNearestUpperLeader(level int, levelMap map[int]hierarchy.LevelState) (node.PeerInfo, bool) {
	if level <= 0 {
		return node.PeerInfo{}, false
	}
	for up := level - 1; up >= 0; up-- {
		st, ok := levelMap[up]
		if !ok {
			continue
		}
		if st.Leader.ID != 0 {
			return st.Leader, true
		}
	}
	return node.PeerInfo{}, false
}

func (m *Manager) rotateLevelLeaderWithChoreography(lv hierarchy.LevelState, best node.PeerInfo, mean float64, cfg StartConfig) bool {
	oldLeaderID := lv.Leader.ID
	newLeaderID := best.ID
	if oldLeaderID == 0 || newLeaderID == 0 || oldLeaderID == newLeaderID {
		return false
	}

	m.mu.Lock()
	if !m.running || m.m == nil || m.hier == nil {
		m.mu.Unlock()
		return false
	}
	sim := m.m
	hier := m.hier
	m.mu.Unlock()

	oldLeader := sim.NodeByID(oldLeaderID)
	newLeader := sim.NodeByID(newLeaderID)
	if oldLeader == nil || newLeader == nil {
		return false
	}

	recipients := m.rotationRecipients()
	if len(recipients) == 0 {
		return false
	}
	trace := connsim.NewTraceID("rot", m.traceSeq.Add(1))
	proposalID := trace + "-proposal"

	prevRev := hier.Snapshot().Revision
	prepare := node.NewWSLeaderRotatePreparePacket(trace+"-prepare", 0, lv.Level, oldLeader.PeerInfo(), newLeader.PeerInfo())
	prepare.ProposalID = proposalID
	prepare.CurrentRevision = prevRev
	prepare.MeanQ = mean
	prepare.OldLeaderQ = lv.Leader.Q
	prepare.CandidateQ = best.Q
	prepare.TriggerDropPct = cfg.RotationDropPct
	prepare.TriggerMinDelta = cfg.RotationMinDelta

	expected := make([]node.NodeID, 0, len(recipients))
	for _, id := range recipients {
		if id == 0 || id == oldLeaderID {
			continue
		}
		expected = append(expected, id)
	}
	quorum := calcPrepareQuorum(len(expected), cfg.RotationPrepareQuorumPct)
	proposal := m.openRotationPrepareProposal(proposalID, expected, quorum)
	if proposal == nil {
		log.Printf("comp=dashboard event=rotate_abort level=%d old=%d new=%d reason=proposal_init_failed trace=%s", lv.Level, oldLeaderID, newLeaderID, trace)
		return false
	}
	defer m.closeRotationPrepareProposal(proposalID)

	m.broadcastWSControl(oldLeaderID, recipients, string(node.WSControlRotatePrepare), prepare, trace+"-prepare")
	log.Printf(
		"comp=dashboard event=rotate_prepare_send level=%d old=%d new=%d recipients=%d expected=%d quorum=%d rev=%d proposal=%s trace=%s",
		lv.Level, oldLeaderID, newLeaderID, len(recipients), len(expected), quorum, prevRev, proposalID, trace,
	)
	if len(expected) > 0 {
		timeout := time.Duration(cfg.RotationPrepareTimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 1200 * time.Millisecond
		}
		select {
		case <-proposal.done:
		case <-time.After(timeout):
		}
		accepted, rejected, reached := proposal.outcome()
		if !reached {
			log.Printf(
				"comp=dashboard event=rotate_abort level=%d old=%d new=%d reason=prepare_quorum_not_reached accepted=%d rejected=%d expected=%d quorum=%d proposal=%s trace=%s",
				lv.Level, oldLeaderID, newLeaderID, accepted, rejected, len(expected), quorum, proposalID, trace,
			)
			return false
		}
		log.Printf(
			"comp=dashboard event=rotate_prepare_quorum_ok level=%d old=%d new=%d accepted=%d rejected=%d expected=%d quorum=%d proposal=%s trace=%s",
			lv.Level, oldLeaderID, newLeaderID, accepted, rejected, len(expected), quorum, proposalID, trace,
		)
	}

	proposedTargetRev := prevRev + 1
	if proposedTargetRev <= prevRev {
		proposedTargetRev = prevRev
	}
	commit := node.NewWSLeaderRotateCommitPacket(trace+"-commit", 0, lv.Level, oldLeader.PeerInfo(), newLeader.PeerInfo())
	commit.ProposalID = proposalID
	commit.PrevRevision = prevRev
	commit.TargetRevision = proposedTargetRev

	expectedCommit := make([]node.NodeID, 0, len(recipients))
	for _, id := range recipients {
		if id == 0 || id == newLeaderID {
			continue
		}
		expectedCommit = append(expectedCommit, id)
	}
	commitQuorum := calcPrepareQuorum(len(expectedCommit), cfg.RotationCommitQuorumPct)
	commitProposal := m.openRotationCommitProposal(proposalID, expectedCommit, commitQuorum)
	if commitProposal == nil {
		log.Printf("comp=dashboard event=rotate_abort level=%d old=%d new=%d reason=commit_proposal_init_failed trace=%s", lv.Level, oldLeaderID, newLeaderID, trace)
		return false
	}
	defer m.closeRotationCommitProposal(proposalID)

	m.broadcastWSControl(newLeaderID, recipients, string(node.WSControlRotateCommit), commit, trace+"-commit")
	log.Printf(
		"comp=dashboard event=rotate_commit_send level=%d old=%d new=%d recipients=%d expected=%d quorum=%d prev_rev=%d target_rev=%d proposal=%s trace=%s",
		lv.Level, oldLeaderID, newLeaderID, len(recipients), len(expectedCommit), commitQuorum, prevRev, proposedTargetRev, proposalID, trace,
	)
	if len(expectedCommit) > 0 {
		timeout := time.Duration(cfg.RotationCommitTimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 1200 * time.Millisecond
		}
		select {
		case <-commitProposal.done:
		case <-time.After(timeout):
		}
		accepted, rejected, reached := commitProposal.outcome()
		if !reached {
			log.Printf(
				"comp=dashboard event=rotate_abort level=%d old=%d new=%d reason=commit_quorum_not_reached accepted=%d rejected=%d expected=%d quorum=%d proposal=%s trace=%s",
				lv.Level, oldLeaderID, newLeaderID, accepted, rejected, len(expectedCommit), commitQuorum, proposalID, trace,
			)
			return false
		} else {
			log.Printf(
				"comp=dashboard event=rotate_commit_quorum_ok level=%d old=%d new=%d accepted=%d rejected=%d expected=%d quorum=%d proposal=%s trace=%s",
				lv.Level, oldLeaderID, newLeaderID, accepted, rejected, len(expectedCommit), commitQuorum, proposalID, trace,
			)
		}
	}

	if !m.rotateLevelLeader(lv.Level, oldLeaderID, newLeaderID) {
		log.Printf("comp=dashboard event=rotate_abort level=%d old=%d new=%d reason=apply_failed_after_commit trace=%s", lv.Level, oldLeaderID, newLeaderID, trace)
		return false
	}

	newRev := hier.Snapshot().Revision
	applied := node.NewWSLeaderRotateAppliedPacket(trace+"-applied", 0, lv.Level, oldLeader.PeerInfo(), newLeader.PeerInfo(), newLeader.PeerInfo())
	applied.FinalRevision = newRev
	m.broadcastWSControl(newLeaderID, recipients, string(node.WSControlRotateApplied), applied, trace+"-applied")
	log.Printf(
		"comp=dashboard event=rotate_applied_send level=%d old=%d new=%d recipients=%d final_rev=%d trace=%s",
		lv.Level, oldLeaderID, newLeaderID, len(recipients), newRev, trace,
	)
	return true
}

func (m *Manager) rotationRecipients() []node.NodeID {
	m.mu.Lock()
	sim := m.m
	m.mu.Unlock()
	if sim == nil {
		return nil
	}
	nodes := sim.NodesSnapshot()
	out := make([]node.NodeID, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || n.ID == 0 || !n.IsRunning() {
			continue
		}
		out = append(out, n.ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (m *Manager) broadcastWSControl(from node.NodeID, targets []node.NodeID, kind string, pkt node.WSPacket, trace string) {
	if from == 0 || len(targets) == 0 || pkt == nil {
		return
	}
	for _, to := range targets {
		if to == 0 || to == from {
			continue
		}
		m.sendWSControl(from, to, kind, pkt, trace)
	}
}

func (m *Manager) sendWSControl(from, to node.NodeID, kind string, pkt node.WSPacket, trace string) bool {
	m.mu.Lock()
	if !m.running || m.wsFabric == nil || m.m == nil {
		m.mu.Unlock()
		return false
	}
	wsFabric := m.wsFabric
	sim := m.m
	m.mu.Unlock()
	if sim.NodeByID(from) == nil || sim.NodeByID(to) == nil {
		return false
	}
	if _, ok := wsFabric.Connect(from, to, trace+"-link"); !ok {
		return false
	}
	payload, err := node.EncodeWSPacket(pkt)
	if err != nil {
		log.Printf("comp=dashboard event=ws_ctrl_encode_err kind=%s from=%d to=%d err=%v trace=%s", kind, from, to, err, trace)
		return false
	}
	if !wsFabric.SendFrame(from, to, kind, payload, trace) {
		log.Printf("comp=dashboard event=ws_ctrl_send_err kind=%s from=%d to=%d trace=%s", kind, from, to, trace)
		return false
	}
	m.addWSSentByType(node.WSControlType(kind))
	return true
}

func (m *Manager) sendRotatePrepareAck(selfNode *node.Node, to node.NodeID, prepare node.WSLeaderRotatePreparePacket, accept bool, reason string, trace string) {
	if selfNode == nil || to == 0 {
		return
	}
	ack := node.NewWSLeaderRotatePrepareAckPacket(trace, 0, prepare.ProposalID, prepare.Level, selfNode.PeerInfo(), accept)
	snap := node.NodeManagerSnapshot{}
	if selfNode.Manager != nil {
		snap = selfNode.Manager.Snapshot()
	}
	ack.KnownRevision = snap.TopologyRevision
	ack.Reason = reason
	if !m.sendWSControl(selfNode.ID, to, string(node.WSControlRotatePrepareAck), ack, trace) {
		log.Printf(
			"comp=dashboard event=rotate_prepare_ack_send_err proposal=%s from=%d to=%d accept=%t reason=%s trace=%s",
			prepare.ProposalID, selfNode.ID, to, accept, reason, trace,
		)
		return
	}
	log.Printf(
		"comp=dashboard event=rotate_prepare_ack_send proposal=%s from=%d to=%d accept=%t reason=%s rev=%d trace=%s",
		prepare.ProposalID, selfNode.ID, to, accept, reason, ack.KnownRevision, trace,
	)
}

func (m *Manager) sendRotateCommitAck(selfNode *node.Node, to node.NodeID, commit node.WSLeaderRotateCommitPacket, accept bool, reason string, trace string) {
	if selfNode == nil || to == 0 {
		return
	}
	ack := node.NewWSLeaderRotateCommitAckPacket(trace, 0, commit.ProposalID, commit.Level, selfNode.PeerInfo(), accept)
	snap := node.NodeManagerSnapshot{}
	if selfNode.Manager != nil {
		snap = selfNode.Manager.Snapshot()
	}
	ack.KnownRevision = snap.TopologyRevision
	ack.Reason = reason
	if !m.sendWSControl(selfNode.ID, to, string(node.WSControlRotateCommitAck), ack, trace) {
		log.Printf(
			"comp=dashboard event=rotate_commit_ack_send_err proposal=%s from=%d to=%d accept=%t reason=%s trace=%s",
			commit.ProposalID, selfNode.ID, to, accept, reason, trace,
		)
		return
	}
	log.Printf(
		"comp=dashboard event=rotate_commit_ack_send proposal=%s from=%d to=%d accept=%t reason=%s rev=%d trace=%s",
		commit.ProposalID, selfNode.ID, to, accept, reason, ack.KnownRevision, trace,
	)
}

func (m *Manager) getRotationPrepareProposal(id string) *rotationPrepareProposal {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rotationPrep == nil {
		return nil
	}
	return m.rotationPrep[id]
}

func (m *Manager) openRotationPrepareProposal(id string, expected []node.NodeID, quorum int) *rotationPrepareProposal {
	if id == "" {
		return nil
	}
	if quorum <= 0 {
		quorum = 1
	}
	exp := make(map[node.NodeID]struct{}, len(expected))
	for _, v := range expected {
		if v == 0 {
			continue
		}
		exp[v] = struct{}{}
	}
	p := &rotationPrepareProposal{
		expected:  exp,
		responded: make(map[node.NodeID]bool, len(exp)),
		accepted:  make(map[node.NodeID]struct{}, len(exp)),
		rejected:  make(map[node.NodeID]string, len(exp)),
		quorum:    quorum,
		done:      make(chan struct{}),
	}
	m.mu.Lock()
	if m.rotationPrep == nil {
		m.rotationPrep = make(map[string]*rotationPrepareProposal)
	}
	m.rotationPrep[id] = p
	m.mu.Unlock()
	return p
}

func (m *Manager) closeRotationPrepareProposal(id string) {
	if id == "" {
		return
	}
	m.mu.Lock()
	delete(m.rotationPrep, id)
	m.mu.Unlock()
}

func (m *Manager) getRotationCommitProposal(id string) *rotationPrepareProposal {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rotationCommit == nil {
		return nil
	}
	return m.rotationCommit[id]
}

func (m *Manager) openRotationCommitProposal(id string, expected []node.NodeID, quorum int) *rotationPrepareProposal {
	if id == "" {
		return nil
	}
	if quorum <= 0 {
		quorum = 1
	}
	exp := make(map[node.NodeID]struct{}, len(expected))
	for _, v := range expected {
		if v == 0 {
			continue
		}
		exp[v] = struct{}{}
	}
	p := &rotationPrepareProposal{
		expected:  exp,
		responded: make(map[node.NodeID]bool, len(exp)),
		accepted:  make(map[node.NodeID]struct{}, len(exp)),
		rejected:  make(map[node.NodeID]string, len(exp)),
		quorum:    quorum,
		done:      make(chan struct{}),
	}
	m.mu.Lock()
	if m.rotationCommit == nil {
		m.rotationCommit = make(map[string]*rotationPrepareProposal)
	}
	m.rotationCommit[id] = p
	m.mu.Unlock()
	return p
}

func (m *Manager) closeRotationCommitProposal(id string) {
	if id == "" {
		return
	}
	m.mu.Lock()
	delete(m.rotationCommit, id)
	m.mu.Unlock()
}

func (p *rotationPrepareProposal) addAck(from node.NodeID, accept bool, reason string) (int, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.expected[from]; !ok {
		return len(p.accepted), len(p.rejected), len(p.accepted) >= p.quorum
	}
	if _, dup := p.responded[from]; dup {
		return len(p.accepted), len(p.rejected), len(p.accepted) >= p.quorum
	}
	p.responded[from] = accept
	if accept {
		p.accepted[from] = struct{}{}
	} else {
		p.rejected[from] = reason
	}
	reached := len(p.accepted) >= p.quorum
	allDone := len(p.responded) >= len(p.expected)
	if reached || allDone {
		p.doneOnce.Do(func() {
			close(p.done)
		})
	}
	return len(p.accepted), len(p.rejected), reached
}

func (p *rotationPrepareProposal) outcome() (accepted int, rejected int, reached bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	accepted = len(p.accepted)
	rejected = len(p.rejected)
	reached = accepted >= p.quorum
	return
}

func calcPrepareQuorum(expected int, pct float64) int {
	if expected <= 0 {
		return 0
	}
	if pct <= 0 || pct > 1 {
		pct = 0.67
	}
	q := int(math.Ceil(float64(expected) * pct))
	if q < 1 {
		q = 1
	}
	if q > expected {
		q = expected
	}
	return q
}

func (m *Manager) rotateLevelLeader(level int, oldLeaderID, newLeaderID node.NodeID) bool {
	m.placementMu.Lock()
	defer m.placementMu.Unlock()

	m.mu.Lock()
	if !m.running || m.m == nil || m.hier == nil {
		m.mu.Unlock()
		return false
	}
	sim := m.m
	hier := m.hier
	cfg := m.cfg
	m.mu.Unlock()

	oldLeader := sim.NodeByID(oldLeaderID)
	newLeader := sim.NodeByID(newLeaderID)
	if oldLeader == nil || newLeader == nil {
		return false
	}
	if oldLeader.Level != level || newLeader.Level != level {
		return false
	}

	oldLeader.SetRole(node.RoleBase)
	if oldLeader.Metrics.Q >= 0.7 {
		oldLeader.SetRole(node.RoleSender)
	}
	newLeader.SetRole(node.RoleLeader)

	updated := make([]node.PeerInfo, 0, len(sim.NodesSnapshot()))
	for _, n := range sim.NodesSnapshot() {
		if n == nil {
			continue
		}
		updated = append(updated, n.PeerInfo())
	}
	hs := hier.RebuildFromPeers(updated)
	m.syncManagersFromHierarchy(hs, cfg.Y)
	return true
}

func (m *Manager) rebuildHierarchy() hierarchy.Snapshot {
	m.mu.Lock()
	sim := m.m
	hier := m.hier
	m.mu.Unlock()
	if sim == nil || hier == nil {
		return hierarchy.Snapshot{}
	}

	nodes := sim.NodesSnapshot()
	peers := make([]node.PeerInfo, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		peers = append(peers, n.PeerInfo())
	}
	snap := hier.RebuildFromPeers(peers)
	total := snap.TotalLevels
	rev := snap.Revision
	if total <= 0 {
		total = 1
	}
	for _, n := range nodes {
		if n == nil || n.Manager == nil {
			continue
		}
		n.Manager.SetHierarchyPosition(n.Level, total, rev)
	}
	return snap
}

func buildTopologyPathToRoot(id node.NodeID, parent map[node.NodeID]node.NodeID, topo map[node.NodeID]string) []string {
	if id == 0 {
		return nil
	}
	visited := make(map[node.NodeID]struct{}, 8)
	path := make([]string, 0, 8)
	cur := id
	for cur != 0 {
		if _, loop := visited[cur]; loop {
			break
		}
		visited[cur] = struct{}{}
		if t := topo[cur]; t != "" {
			path = append(path, t)
		} else {
			path = append(path, "")
		}
		cur = parent[cur]
	}
	return path
}

func nextLeaderTopologyID(parentTopo string, slot int) string {
	if slot <= 0 {
		slot = 1
	}
	if parentTopo == "" || parentTopo == "0" {
		return "1-" + strconv.Itoa(slot)
	}
	return parentTopo + "-" + strconv.Itoa(slot)
}

func nextFollowerTopologyID(leaderTopo string, slot int) string {
	if slot <= 0 {
		slot = 1
	}
	if leaderTopo == "" {
		leaderTopo = "0"
	}
	return leaderTopo + "-b" + strconv.Itoa(slot)
}

func betterPeer(a, b node.PeerInfo) bool {
	if a.Q == b.Q {
		return a.ID < b.ID
	}
	return a.Q > b.Q
}

func sortNodesByQuality(nodes []*node.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		pi := nodes[i].PeerInfo()
		pj := nodes[j].PeerInfo()
		return betterPeer(pi, pj)
	})
}

func limitEdgeSnapshotsForAPI(edges []connsim.EdgeSnapshot, max int) []connsim.EdgeSnapshot {
	if max <= 0 || len(edges) <= max {
		return edges
	}
	out := append([]connsim.EdgeSnapshot(nil), edges...)
	kindRank := func(kind string) int {
		switch kind {
		case "CONNECT":
			return 0
		case "OFFER":
			return 1
		case "DISCOVER":
			return 2
		default:
			return 3
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := kindRank(out[i].Kind), kindRank(out[j].Kind)
		if ri != rj {
			return ri < rj
		}
		if out[i].LastSeenMS != out[j].LastSeenMS {
			return out[i].LastSeenMS > out[j].LastSeenMS
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out[:max]
}

func (m *Manager) reparentCooldown(cfg StartConfig) time.Duration {
	if cfg.MinReparentIntervalMS <= 0 {
		return 0
	}
	return time.Duration(cfg.MinReparentIntervalMS) * time.Millisecond
}

func (m *Manager) shouldThrottleReparent(nodeID node.NodeID, prevLeaderID, newLeaderID node.NodeID, wasConnected bool, cfg StartConfig, now time.Time) (bool, time.Duration) {
	cooldown := m.reparentCooldown(cfg)
	if cooldown <= 0 || nodeID == 0 || prevLeaderID == 0 || newLeaderID == 0 || prevLeaderID == newLeaderID || !wasConnected {
		return false, 0
	}
	m.mu.Lock()
	last := m.reparentAt[nodeID]
	m.mu.Unlock()
	if last.IsZero() {
		return false, 0
	}
	elapsed := now.Sub(last)
	if elapsed >= cooldown {
		return false, 0
	}
	return true, cooldown - elapsed
}

func (m *Manager) removeReparentTracking(ids []node.NodeID) {
	if len(ids) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		delete(m.reparentAt, id)
	}
}

func (m *Manager) qDriftFreezeRemaining(now time.Time) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.qFreezeUntil.IsZero() {
		return 0
	}
	if now.After(m.qFreezeUntil) {
		m.qFreezeUntil = time.Time{}
		return 0
	}
	return m.qFreezeUntil.Sub(now)
}

func (m *Manager) applyQDriftFreeze(now time.Time, d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	if !m.running || m.m == nil {
		m.mu.Unlock()
		return
	}
	until := now.Add(d)
	if until.After(m.qFreezeUntil) {
		m.qFreezeUntil = until
	}
	sim := m.m
	m.mu.Unlock()

	for _, n := range sim.NodesSnapshot() {
		if n == nil {
			continue
		}
		n.FreezeQDriftFor(d)
	}
}

func (m *Manager) tryConnect(from, target node.NodeID, trace string) {
	if from == 0 || target == 0 || from == target {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return
	}
	prevTarget, connected := m.connected[from]
	if connected && prevTarget == target {
		// keep going to refresh local manager state idempotently.
	}
	m.connected[from] = target
	switch {
	case !connected:
		log.Printf("comp=dashboard event=connect_established from=%d to=%d trace=%s", from, target, trace)
	case prevTarget != target:
		log.Printf("comp=dashboard event=connect_reparent from=%d old_to=%d new_to=%d trace=%s", from, prevTarget, target, trace)
	default:
		log.Printf("comp=dashboard event=connect_refresh from=%d to=%d trace=%s", from, target, trace)
	}
	if !connected || prevTarget != target {
		if m.reparentAt == nil {
			m.reparentAt = make(map[node.NodeID]time.Time)
		}
		m.reparentAt[from] = time.Now()
	}

	if m.m != nil {
		fromNode := m.m.NodeByID(from)
		targetNode := m.m.NodeByID(target)
		if fromNode != nil && targetNode != nil && fromNode.Manager != nil {
			fromNode.Manager.UpsertActiveConnection(targetNode.PeerInfo())
			fromNode.Manager.SetCurrentLeader(targetNode.PeerInfo())
			fromNode.Manager.SetConnectionState(node.ConnStateConnected)
		}
		if targetNode != nil && fromNode != nil && targetNode.Manager != nil {
			targetNode.Manager.UpsertActiveConnection(fromNode.PeerInfo())
			if targetNode.Manager.Snapshot().ConnectionState == node.ConnStateSolo {
				targetNode.Manager.SetConnectionState(node.ConnStateConnected)
			}
		}
	}

	// Mark a logical connection edge for visualization.
	if m.fabric != nil {
		if m.fabric.Send(from, target, "CONNECT", []byte("connect"), trace+"-connect") {
			m.addConnectSent(1)
		}
	}
}

func (m *Manager) unconnectedNodeIDs() []node.NodeID {
	m.mu.Lock()
	fabric := m.fabric
	sim := m.m
	m.mu.Unlock()

	if fabric == nil {
		return nil
	}

	// Limit candidates to endpoints already registered in the UDP fabric, so
	// newly created-but-not-registered nodes do not emit unknown_sender drops.
	snap := fabric.Snapshot()
	out := make([]node.NodeID, 0, len(snap.Nodes))
	for _, ep := range snap.Nodes {
		if sim == nil {
			out = append(out, ep.NodeID)
			continue
		}
		n := sim.NodeByID(ep.NodeID)
		if n == nil {
			continue
		}
		if m.nodeNeedsDiscovery(n) {
			out = append(out, ep.NodeID)
		}
	}
	return out
}

func (m *Manager) nodeNeedsDiscovery(n *node.Node) bool {
	if n == nil || n.Manager == nil {
		return true
	}
	snap := n.Manager.Snapshot()
	isRootLeader := n.Role == node.RoleLeader && n.Level == 0
	hasLeaderPath := snap.CurrentLeader.ID != 0
	hasActiveConn := len(snap.ActiveConnections) > 0

	disconnected := false
	switch n.Role {
	case node.RoleLeader:
		disconnected = !isRootLeader && n.Level > 0 && !hasLeaderPath && !hasActiveConn
	default:
		disconnected = !hasLeaderPath && !hasActiveConn
	}
	if disconnected {
		if snap.ConnectionState != node.ConnStateDisconnected {
			n.Manager.SetConnectionState(node.ConnStateDisconnected)
			log.Printf(
				"comp=dashboard event=node_disconnected_detected node=%d role=%s level=%d state=%s",
				n.ID, n.Role.String(), n.Level, snap.ConnectionState.String(),
			)
		}
		return true
	}

	if snap.ConnectionState == node.ConnStateSolo || snap.ConnectionState == node.ConnStateDisconnected {
		return true
	}
	if (snap.ConnectionState == node.ConnStateJoining || snap.ConnectionState == node.ConnStateTransitioning) &&
		!hasLeaderPath && !hasActiveConn && !isRootLeader {
		return true
	}
	return false
}

func (m *Manager) sendRandomDiscoveryOnce(fabric *connsim.Fabric, sender node.NodeID, kind string, payload []byte, fanout int) int {
	if fabric == nil || sender == 0 {
		return 0
	}
	targets := m.pickRandomTargets(sender, fabricNodeIDs(fabric), fanout)
	return m.sendDiscoverBurst(fabric, sender, targets, kind, payload)
}

func (m *Manager) reconcileDiscoveryState(unconnected []node.NodeID) {
	m.discMu.Lock()
	defer m.discMu.Unlock()
	if len(m.discState) == 0 {
		return
	}
	live := make(map[node.NodeID]struct{}, len(unconnected))
	for _, id := range unconnected {
		if id != 0 {
			live[id] = struct{}{}
		}
	}
	for id := range m.discState {
		if _, ok := live[id]; !ok {
			delete(m.discState, id)
		}
	}
}

func (m *Manager) canSendDiscoveryNow(id node.NodeID, now time.Time) bool {
	m.discMu.Lock()
	defer m.discMu.Unlock()
	st, ok := m.discState[id]
	if !ok || st.NextAt.IsZero() {
		return true
	}
	return !now.Before(st.NextAt)
}

func (m *Manager) updateDiscoveryBackoff(id node.NodeID, sent int, now time.Time, cfg StartConfig) {
	if id == 0 {
		return
	}
	m.discMu.Lock()
	defer m.discMu.Unlock()
	st := m.discState[id]
	if sent > 0 {
		st.Attempt++
		if st.Attempt > 16 {
			st.Attempt = 16
		}
	} else if st.Attempt <= 0 {
		st.Attempt = 1
	}
	delay := computeDiscoveryBackoff(st.Attempt, cfg.RandomDiscoverBackoffBaseMS, cfg.RandomDiscoverBackoffMaxMS)
	if j := m.randomJitterDuration(cfg.RandomDiscoverBackoffJitterMS); j > 0 {
		delay += j
	}
	st.NextAt = now.Add(delay)
	m.discState[id] = st
}

func computeDiscoveryBackoff(attempt int, baseMS, maxMS int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if baseMS <= 0 {
		baseMS = 500
	}
	if maxMS <= 0 {
		maxMS = 8000
	}
	backoff := baseMS
	for i := 1; i < attempt; i++ {
		if backoff >= maxMS {
			backoff = maxMS
			break
		}
		backoff *= 2
		if backoff > maxMS {
			backoff = maxMS
			break
		}
	}
	return time.Duration(backoff) * time.Millisecond
}

func (m *Manager) randomJitterDuration(jitterMS int) time.Duration {
	if jitterMS <= 0 {
		return 0
	}
	m.mu.Lock()
	r := m.discRng
	m.mu.Unlock()
	if r == nil {
		return 0
	}
	m.discRngMu.Lock()
	v := r.Intn(jitterMS + 1)
	m.discRngMu.Unlock()
	return time.Duration(v) * time.Millisecond
}

func (m *Manager) pickRandomTargets(sender node.NodeID, all []node.NodeID, fanout int) []node.NodeID {
	if fanout <= 0 {
		fanout = 1
	}
	if len(all) <= 1 {
		return nil
	}
	candidates := make([]node.NodeID, 0, len(all)-1)
	for _, id := range all {
		if id == 0 || id == sender {
			continue
		}
		candidates = append(candidates, id)
	}
	if len(candidates) == 0 {
		return nil
	}
	m.shuffleNodeIDs(candidates)
	if fanout > len(candidates) {
		fanout = len(candidates)
	}
	return append([]node.NodeID(nil), candidates[:fanout]...)
}

func (m *Manager) sendDiscoverBurst(fabric *connsim.Fabric, sender node.NodeID, targets []node.NodeID, kind string, payload []byte) int {
	if fabric == nil || sender == 0 || len(targets) == 0 {
		return 0
	}
	sent := 0
	for _, to := range targets {
		if to == 0 || to == sender {
			continue
		}
		trace := connsim.NewTraceID("udp", m.traceSeq.Add(1))
		if fabric.Send(sender, to, kind, payload, trace) {
			sent++
		}
	}
	if sent > 0 {
		m.addDiscoverSent(uint64(sent))
	}
	return sent
}

func fabricNodeIDs(fabric *connsim.Fabric) []node.NodeID {
	if fabric == nil {
		return nil
	}
	snap := fabric.Snapshot()
	out := make([]node.NodeID, 0, len(snap.Nodes))
	for _, ep := range snap.Nodes {
		if ep.NodeID != 0 {
			out = append(out, ep.NodeID)
		}
	}
	return out
}

func (m *Manager) allowOffer(id node.NodeID) bool {
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()
	if cfg.OfferRateLimitPerSec <= 0 || cfg.OfferBurst <= 0 {
		return true
	}
	now := time.Now()
	rate := float64(cfg.OfferRateLimitPerSec)
	burst := float64(cfg.OfferBurst)

	m.discMu.Lock()
	defer m.discMu.Unlock()
	tb, ok := m.offerTB[id]
	if !ok {
		tb = offerTokenBucket{
			Tokens: burst,
			Last:   now,
		}
	}
	if !tb.Last.IsZero() {
		elapsed := now.Sub(tb.Last).Seconds()
		tb.Tokens += elapsed * rate
		if tb.Tokens > burst {
			tb.Tokens = burst
		}
	}
	tb.Last = now
	if tb.Tokens < 1 {
		m.offerTB[id] = tb
		return false
	}
	tb.Tokens -= 1
	m.offerTB[id] = tb
	return true
}

func (m *Manager) udpSenderMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.UDPSender
}

func (m *Manager) ensureRootNodeID() node.NodeID {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running || m.m == nil {
		return 0
	}

	isAlive := func(id node.NodeID) bool {
		if id == 0 {
			return false
		}
		n := m.m.NodeByID(id)
		return n != nil && n.IsRunning()
	}
	if isAlive(m.rootNodeID) {
		return m.rootNodeID
	}

	// Prefer explicit root topology marker when available.
	for id, topo := range m.topologyByNode {
		if topo == "0" && isAlive(id) {
			if m.rootNodeID != id {
				log.Printf("comp=dashboard event=root_elected root=%d reason=topology", id)
			}
			m.rootNodeID = id
			return id
		}
	}

	// Deterministic fallback: lowest running NodeID.
	var minID node.NodeID
	for _, n := range m.m.NodesSnapshot() {
		if n == nil || !n.IsRunning() {
			continue
		}
		if minID == 0 || n.ID < minID {
			minID = n.ID
		}
	}
	if minID != 0 && m.rootNodeID != minID {
		log.Printf("comp=dashboard event=root_elected root=%d reason=min_id_fallback", minID)
	}
	m.rootNodeID = minID
	return minID
}

func (m *Manager) shuffleNodeIDs(ids []node.NodeID) {
	if len(ids) < 2 {
		return
	}
	m.mu.Lock()
	r := m.discRng
	m.mu.Unlock()
	if r == nil {
		return
	}
	m.discRngMu.Lock()
	r.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	m.discRngMu.Unlock()
}
