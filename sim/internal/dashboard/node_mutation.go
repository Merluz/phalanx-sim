package dashboard

import (
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"phalanxsim2/internal/node"
	"phalanxsim2/internal/node/connsim"
)

// SpawnNodesRequest configures dynamic node creation.
type SpawnNodesRequest struct {
	Count        int  `json:"count"`
	Async        bool `json:"async"`
	Parallelism  int  `json:"parallelism"`
	SpawnDelayMS int  `json:"spawn_delay_ms"`
}

// SpawnNodesResult reports spawn operation outcome.
type SpawnNodesResult struct {
	OK          bool          `json:"ok"`
	Requested   int           `json:"requested"`
	Spawned     int           `json:"spawned"`
	BeforeTotal int           `json:"before_total"`
	AfterTotal  int           `json:"after_total"`
	AddedIDs    []node.NodeID `json:"added_ids,omitempty"`
}

// KillNodesRequest configures dynamic node removal.
type KillNodesRequest struct {
	Mode            string        `json:"mode"`        // count|percent|ids
	TargetRole      string        `json:"target_role"` // all|leader|sender|base
	Count           int           `json:"count"`
	Percent         float64       `json:"percent"`
	IDs             []node.NodeID `json:"ids"`
	ExcludeRoot     bool          `json:"exclude_root"`
	MinLeadersKeep  int           `json:"min_leaders_keep"`
	RecoveryTrigger string        `json:"-"`
	DisableRecovery bool          `json:"-"`
}

// KillNodesResult reports kill operation outcome.
type KillNodesResult struct {
	OK             bool          `json:"ok"`
	Requested      int           `json:"requested"`
	TargetRole     string        `json:"target_role"`
	RemovedIDs     []node.NodeID `json:"removed_ids"`
	RemovedCount   int           `json:"removed_count"`
	RemovedLeaders int           `json:"removed_leaders"`
	RemovedSenders int           `json:"removed_senders"`
	RemovedBase    int           `json:"removed_base"`
	BeforeTotal    int           `json:"before_total"`
	AfterTotal     int           `json:"after_total"`
}

// RestartNodesRequest configures remove+recreate flow for selected IDs.
type RestartNodesRequest struct {
	IDs         []node.NodeID `json:"ids"`
	StaggerMS   int           `json:"stagger_ms"`
	ExcludeRoot bool          `json:"exclude_root"`
}

// RestartNodesResult reports restart operation outcome.
type RestartNodesResult struct {
	OK          bool          `json:"ok"`
	Requested   int           `json:"requested"`
	Restarted   int           `json:"restarted"`
	RemovedIDs  []node.NodeID `json:"removed_ids"`
	AddedIDs    []node.NodeID `json:"added_ids"`
	BeforeTotal int           `json:"before_total"`
	AfterTotal  int           `json:"after_total"`
}

// RestartRandomNodesRequest configures random remove+recreate flow.
type RestartRandomNodesRequest struct {
	Mode        string  `json:"mode"` // count|percent
	Count       int     `json:"count"`
	Percent     float64 `json:"percent"`
	StaggerMS   int     `json:"stagger_ms"`
	ExcludeRoot bool    `json:"exclude_root"`
}

// SpawnNodes adds nodes to the running simulation instance.
func (m *Manager) SpawnNodes(req SpawnNodesRequest) (SpawnNodesResult, error) {
	if req.Count <= 0 {
		return SpawnNodesResult{}, errors.New("count must be > 0")
	}
	if req.Parallelism < 0 {
		return SpawnNodesResult{}, errors.New("parallelism must be >= 0")
	}
	if req.SpawnDelayMS < 0 {
		return SpawnNodesResult{}, errors.New("spawn_delay_ms must be >= 0")
	}

	m.mu.Lock()
	if !m.running || m.m == nil || m.fabric == nil || m.wsFabric == nil || m.runCtx == nil {
		m.mu.Unlock()
		return SpawnNodesResult{}, errors.New("simulation not running")
	}
	sim := m.m
	fabric := m.fabric
	wsFabric := m.wsFabric
	cfg := m.cfg
	runCtx := m.runCtx
	before := len(sim.NodesSnapshot())
	m.mu.Unlock()

	waitDelay := func() error {
		if req.SpawnDelayMS <= 0 {
			return nil
		}
		timer := time.NewTimer(time.Duration(req.SpawnDelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-runCtx.Done():
			return runCtx.Err()
		case <-timer.C:
			return nil
		}
	}

	var addedMu sync.Mutex
	addedIDs := make([]node.NodeID, 0, req.Count)

	spawnOne := func() error {
		n, err := sim.AddNodeUnstarted(runCtx)
		if err != nil {
			return err
		}
		subnet := connsim.NodeSubnetForIndex(n.ID, cfg.UDPSubnets)
		if n.Manager != nil {
			n.Manager.SetConnectionState(node.ConnStateSolo)
		}
		n.SetOnMessage(m.buildNodeHandler(n.ID))
		fabric.RegisterNode(n, subnet)
		wsFabric.RegisterNode(n)
		if freezeRemaining := m.qDriftFreezeRemaining(time.Now()); freezeRemaining > 0 {
			n.FreezeQDriftFor(freezeRemaining)
		}
		sim.StartNode(n)
		addedMu.Lock()
		addedIDs = append(addedIDs, n.ID)
		addedMu.Unlock()
		if cfg.UDPDemo && cfg.UDPSender == "random" {
			sent := m.sendRandomDiscoveryOnce(fabric, n.ID, cfg.UDPKind, []byte(cfg.UDPPayload), cfg.RandomDiscoverFanout)
			m.updateDiscoveryBackoff(n.ID, sent, time.Now(), cfg)
		}
		return nil
	}

	var spawned atomic.Uint64
	if !req.Async {
		for i := 0; i < req.Count; i++ {
			if err := waitDelay(); err != nil {
				return SpawnNodesResult{}, err
			}
			if err := spawnOne(); err != nil {
				return SpawnNodesResult{}, err
			}
			spawned.Add(1)
		}
	} else {
		parallelism := req.Parallelism
		if parallelism <= 0 {
			parallelism = cfg.SpawnParallelism
		}
		if parallelism <= 0 {
			parallelism = 8
		}
		sem := make(chan struct{}, parallelism)
		var wg sync.WaitGroup
		errCh := make(chan error, 1)

		for i := 0; i < req.Count; i++ {
			if err := waitDelay(); err != nil {
				return SpawnNodesResult{}, err
			}
			select {
			case <-runCtx.Done():
				return SpawnNodesResult{}, runCtx.Err()
			default:
			}
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				if err := spawnOne(); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				spawned.Add(1)
			}()
		}
		wg.Wait()
		select {
		case err := <-errCh:
			return SpawnNodesResult{}, err
		default:
		}
	}

	_ = m.applyPlacementCascade(0, 0, node.DefaultNodeManagerConfig().SenderThreshold)
	m.mu.Lock()
	if m.m != nil {
		m.stats = m.m.Stats()
	}
	after := len(sim.NodesSnapshot())
	m.mu.Unlock()

	return SpawnNodesResult{
		OK:          true,
		Requested:   req.Count,
		Spawned:     int(spawned.Load()),
		BeforeTotal: before,
		AfterTotal:  after,
		AddedIDs:    addedIDs,
	}, nil
}

// KillNodes removes nodes from the running simulation instance.
func (m *Manager) KillNodes(req KillNodesRequest) (KillNodesResult, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		switch {
		case len(req.IDs) > 0:
			mode = "ids"
		case req.Percent > 0:
			mode = "percent"
		default:
			mode = "count"
		}
	}
	if mode != "ids" && mode != "count" && mode != "percent" {
		return KillNodesResult{}, errors.New("mode must be one of: ids,count,percent")
	}
	targetRole := strings.ToLower(strings.TrimSpace(req.TargetRole))
	if targetRole == "" {
		targetRole = "all"
	}
	if targetRole != "all" && targetRole != "leader" && targetRole != "sender" && targetRole != "base" {
		return KillNodesResult{}, errors.New("target_role must be one of: all,leader,sender,base")
	}
	minLeadersKeep := req.MinLeadersKeep
	if minLeadersKeep < 0 {
		return KillNodesResult{}, errors.New("min_leaders_keep must be >= 0")
	}

	m.mu.Lock()
	if !m.running || m.m == nil {
		m.mu.Unlock()
		return KillNodesResult{}, errors.New("simulation not running")
	}
	sim := m.m
	fabric := m.fabric
	wsFabric := m.wsFabric
	beforeNodes := sim.NodesSnapshot()
	before := len(beforeNodes)
	rootID := node.NodeID(0)
	for id, topo := range m.topologyByNode {
		if topo == "0" {
			rootID = id
			break
		}
	}
	m.mu.Unlock()

	if before == 0 {
		return KillNodesResult{OK: true}, nil
	}

	candidates := make([]node.NodeID, 0, len(beforeNodes))
	existing := make(map[node.NodeID]struct{}, len(beforeNodes))
	roleByID := make(map[node.NodeID]string, len(beforeNodes))
	for _, n := range beforeNodes {
		if n == nil {
			continue
		}
		existing[n.ID] = struct{}{}
		role := n.Role.String()
		roleByID[n.ID] = role
		if req.ExcludeRoot && rootID != 0 && n.ID == rootID {
			continue
		}
		if targetRole != "all" && role != targetRole {
			continue
		}
		candidates = append(candidates, n.ID)
	}
	if len(candidates) == 0 {
		return KillNodesResult{
			OK:          true,
			Requested:   0,
			TargetRole:  targetRole,
			BeforeTotal: before,
			AfterTotal:  before,
		}, nil
	}

	leaderCandidateCount := 0
	for _, id := range candidates {
		if roleByID[id] == "leader" {
			leaderCandidateCount++
		}
	}

	selected := make([]node.NodeID, 0)
	switch mode {
	case "ids":
		seen := make(map[node.NodeID]struct{}, len(req.IDs))
		for _, id := range req.IDs {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			if _, ok := existing[id]; !ok {
				continue
			}
			if req.ExcludeRoot && rootID != 0 && id == rootID {
				continue
			}
			role := roleByID[id]
			if targetRole != "all" && role != targetRole {
				continue
			}
			selected = append(selected, id)
		}
		if targetRole == "leader" {
			maxKill := leaderCandidateCount - minLeadersKeep
			if maxKill < 0 {
				maxKill = 0
			}
			if len(selected) > maxKill {
				return KillNodesResult{}, errors.New("kill request would violate min_leaders_keep")
			}
		}
	case "count":
		if req.Count <= 0 {
			return KillNodesResult{}, errors.New("count must be > 0")
		}
		m.shuffleNodeIDs(candidates)
		count := req.Count
		if targetRole == "leader" {
			maxKill := leaderCandidateCount - minLeadersKeep
			if maxKill < 0 {
				maxKill = 0
			}
			if count > maxKill {
				count = maxKill
			}
		}
		if count > len(candidates) {
			count = len(candidates)
		}
		selected = append(selected, candidates[:count]...)
	case "percent":
		if req.Percent <= 0 || req.Percent > 100 {
			return KillNodesResult{}, errors.New("percent must be in (0,100]")
		}
		m.shuffleNodeIDs(candidates)
		count := int(math.Ceil((req.Percent / 100.0) * float64(len(candidates))))
		allowZero := false
		if targetRole == "leader" {
			maxKill := leaderCandidateCount - minLeadersKeep
			if maxKill < 0 {
				maxKill = 0
			}
			if count > maxKill {
				count = maxKill
			}
			if maxKill == 0 {
				allowZero = true
			}
		}
		if count < 1 && !allowZero {
			count = 1
		}
		if count > len(candidates) {
			count = len(candidates)
		}
		selected = append(selected, candidates[:count]...)
	}

	if len(selected) == 0 {
		return KillNodesResult{
			OK:          true,
			Requested:   0,
			TargetRole:  targetRole,
			BeforeTotal: before,
			AfterTotal:  before,
		}, nil
	}

	removed := make([]node.NodeID, 0, len(selected))
	removedLeaders := 0
	removedSenders := 0
	removedBase := 0
	m.mu.Lock()
	currentRoot := m.rootNodeID
	m.mu.Unlock()
	rootRemoved := false
	for _, id := range selected {
		if fabric != nil {
			fabric.DeregisterNode(id)
		}
		if wsFabric != nil {
			wsFabric.DeregisterNode(id)
		}
		if sim.RemoveNode(id) {
			switch roleByID[id] {
			case "leader":
				removedLeaders++
			case "sender":
				removedSenders++
			default:
				removedBase++
			}
			if currentRoot != 0 && id == currentRoot {
				rootRemoved = true
			}
			removed = append(removed, id)
		}
	}

	if len(removed) > 0 {
		m.addKillCounts(len(removed), removedLeaders, removedSenders, removedBase)
		m.removeJoinTracking(removed)
		m.removeDiscoveryTracking(removed)
		m.removeReparentTracking(removed)
		m.pruneTopologyState(removed)
		if !req.DisableRecovery {
			trigger := req.RecoveryTrigger
			if strings.TrimSpace(trigger) == "" {
				trigger = "kill"
			}
			m.startRecoveryEpoch(trigger, RecoveryKilledView{
				Total:   len(removed),
				Leaders: removedLeaders,
				Senders: removedSenders,
				Base:    removedBase,
			})
		}
		_ = m.applyPlacementCascade(0, 0, node.DefaultNodeManagerConfig().SenderThreshold)
		if rootRemoved {
			m.mu.Lock()
			if m.rootNodeID == currentRoot {
				m.rootNodeID = 0
			}
			m.mu.Unlock()
		}
	}

	m.mu.Lock()
	if m.m != nil {
		m.stats = m.m.Stats()
	}
	after := len(sim.NodesSnapshot())
	m.mu.Unlock()

	return KillNodesResult{
		OK:             true,
		Requested:      len(selected),
		TargetRole:     targetRole,
		RemovedIDs:     removed,
		RemovedCount:   len(removed),
		RemovedLeaders: removedLeaders,
		RemovedSenders: removedSenders,
		RemovedBase:    removedBase,
		BeforeTotal:    before,
		AfterTotal:     after,
	}, nil
}

// RestartNodes removes selected nodes then spawns the same amount of new nodes.
// New nodes get fresh IDs and re-enter via normal join/discovery.
func (m *Manager) RestartNodes(req RestartNodesRequest) (RestartNodesResult, error) {
	if len(req.IDs) == 0 {
		return RestartNodesResult{}, errors.New("ids must not be empty")
	}
	if req.StaggerMS < 0 {
		return RestartNodesResult{}, errors.New("stagger_ms must be >= 0")
	}
	killRes, err := m.KillNodes(KillNodesRequest{
		Mode:            "ids",
		IDs:             req.IDs,
		ExcludeRoot:     req.ExcludeRoot,
		DisableRecovery: true,
	})
	if err != nil {
		return RestartNodesResult{}, err
	}
	if killRes.RemovedCount == 0 {
		return RestartNodesResult{
			OK:          true,
			Requested:   len(req.IDs),
			Restarted:   0,
			RemovedIDs:  nil,
			AddedIDs:    nil,
			BeforeTotal: killRes.BeforeTotal,
			AfterTotal:  killRes.AfterTotal,
		}, nil
	}
	m.startRecoveryEpoch("restart", RecoveryKilledView{
		Total:   killRes.RemovedCount,
		Leaders: killRes.RemovedLeaders,
		Senders: killRes.RemovedSenders,
		Base:    killRes.RemovedBase,
	})

	spawnRes, err := m.SpawnNodes(SpawnNodesRequest{
		Count:        killRes.RemovedCount,
		Async:        false,
		Parallelism:  1,
		SpawnDelayMS: req.StaggerMS,
	})
	if err != nil {
		return RestartNodesResult{}, err
	}

	return RestartNodesResult{
		OK:          true,
		Requested:   len(req.IDs),
		Restarted:   spawnRes.Spawned,
		RemovedIDs:  killRes.RemovedIDs,
		AddedIDs:    spawnRes.AddedIDs,
		BeforeTotal: killRes.BeforeTotal,
		AfterTotal:  spawnRes.AfterTotal,
	}, nil
}

// RestartRandomNodes removes random nodes (count/percent) then spawns the same amount.
func (m *Manager) RestartRandomNodes(req RestartRandomNodesRequest) (RestartNodesResult, error) {
	if req.StaggerMS < 0 {
		return RestartNodesResult{}, errors.New("stagger_ms must be >= 0")
	}
	mode := req.Mode
	if mode == "" {
		if req.Percent > 0 {
			mode = "percent"
		} else {
			mode = "count"
		}
	}
	if mode != "count" && mode != "percent" {
		return RestartNodesResult{}, errors.New("mode must be one of: count,percent")
	}
	killReq := KillNodesRequest{
		Mode:            mode,
		Count:           req.Count,
		Percent:         req.Percent,
		ExcludeRoot:     req.ExcludeRoot,
		DisableRecovery: true,
	}
	killRes, err := m.KillNodes(killReq)
	if err != nil {
		return RestartNodesResult{}, err
	}
	if killRes.RemovedCount == 0 {
		return RestartNodesResult{
			OK:          true,
			Requested:   killRes.Requested,
			Restarted:   0,
			RemovedIDs:  nil,
			AddedIDs:    nil,
			BeforeTotal: killRes.BeforeTotal,
			AfterTotal:  killRes.AfterTotal,
		}, nil
	}
	m.startRecoveryEpoch("restart_random", RecoveryKilledView{
		Total:   killRes.RemovedCount,
		Leaders: killRes.RemovedLeaders,
		Senders: killRes.RemovedSenders,
		Base:    killRes.RemovedBase,
	})

	spawnRes, err := m.SpawnNodes(SpawnNodesRequest{
		Count:        killRes.RemovedCount,
		Async:        false,
		Parallelism:  1,
		SpawnDelayMS: req.StaggerMS,
	})
	if err != nil {
		return RestartNodesResult{}, err
	}

	return RestartNodesResult{
		OK:          true,
		Requested:   killRes.Requested,
		Restarted:   spawnRes.Spawned,
		RemovedIDs:  killRes.RemovedIDs,
		AddedIDs:    spawnRes.AddedIDs,
		BeforeTotal: killRes.BeforeTotal,
		AfterTotal:  spawnRes.AfterTotal,
	}, nil
}

func (m *Manager) removeJoinTracking(ids []node.NodeID) {
	if len(ids) == 0 {
		return
	}
	m.metricsMu.Lock()
	defer m.metricsMu.Unlock()
	for _, id := range ids {
		delete(m.joinStarted, id)
	}
}

func (m *Manager) removeDiscoveryTracking(ids []node.NodeID) {
	if len(ids) == 0 {
		return
	}
	m.discMu.Lock()
	defer m.discMu.Unlock()
	for _, id := range ids {
		delete(m.discState, id)
		delete(m.offerTB, id)
	}
}

func (m *Manager) pruneTopologyState(ids []node.NodeID) {
	if len(ids) == 0 {
		return
	}
	rm := make(map[node.NodeID]struct{}, len(ids))
	for _, id := range ids {
		rm[id] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		delete(m.topologyByNode, id)
		delete(m.parentByNode, id)
		delete(m.childLeadersByNode, id)
		delete(m.followersByLeader, id)
		delete(m.connected, id)
	}
	for from, to := range m.connected {
		if _, dropFrom := rm[from]; dropFrom {
			delete(m.connected, from)
			continue
		}
		if _, dropTo := rm[to]; dropTo {
			delete(m.connected, from)
		}
	}
	for leader, childs := range m.childLeadersByNode {
		m.childLeadersByNode[leader] = filterOutIDs(childs, rm)
	}
	for leader, followers := range m.followersByLeader {
		m.followersByLeader[leader] = filterOutIDs(followers, rm)
	}
}

func filterOutIDs(in []node.NodeID, rm map[node.NodeID]struct{}) []node.NodeID {
	if len(in) == 0 || len(rm) == 0 {
		return in
	}
	out := make([]node.NodeID, 0, len(in))
	for _, id := range in {
		if _, drop := rm[id]; drop {
			continue
		}
		out = append(out, id)
	}
	return out
}
