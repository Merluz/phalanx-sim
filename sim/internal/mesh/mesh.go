package mesh

import (
	"context"
	"errors"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"phalanxsim2/internal/network"
	"phalanxsim2/internal/node"
)

// Stats is a lightweight runtime snapshot.
type Stats struct {
	TotalNodes   int `json:"total_nodes"`
	Leaders      int `json:"leaders"`
	Senders      int `json:"senders"`
	Base         int `json:"base"`
	RunningNodes int `json:"running_nodes"`
	Subnets      int `json:"subnets"`
}

// Mesh is a zero-logic scaffold around node lifecycle + runtime orchestration.
type Mesh struct {
	cfg Config
	net *network.SimNetwork

	rng   *rand.Rand
	rngMu sync.Mutex

	mu    sync.RWMutex
	nodes map[node.NodeID]*node.Node

	nextID atomic.Uint64
	rootID node.NodeID

	nodeWG sync.WaitGroup
}

// New creates an empty simulation scaffold.
func New(cfg Config, rng *rand.Rand) *Mesh {
	if cfg.TickPeriod <= 0 {
		cfg.TickPeriod = 50 * time.Millisecond
	}
	if cfg.InboxSize <= 0 {
		cfg.InboxSize = 128
	}
	if cfg.SubnetSize == 0 {
		cfg.SubnetSize = 1 << 24
	}
	if cfg.SpawnDelay < 0 {
		cfg.SpawnDelay = 0
	}
	if cfg.QDriftMaxPerSec < 0 {
		cfg.QDriftMaxPerSec = 0
	}
	if cfg.QUpdateInterval <= 0 {
		cfg.QUpdateInterval = time.Second
	}
	return &Mesh{
		cfg:   cfg,
		net:   network.New(cfg.SubnetSize),
		rng:   rng,
		nodes: make(map[node.NodeID]*node.Node),
	}
}

func (m *Mesh) runtimeConfig() node.RuntimeConfig {
	rc := node.DefaultRuntimeConfig()
	rc.TickPeriod = m.cfg.TickPeriod
	rc.InboxSize = m.cfg.InboxSize
	rc.QDriftMaxPerSec = m.cfg.QDriftMaxPerSec
	rc.QUpdateInterval = m.cfg.QUpdateInterval
	return rc
}

func (m *Mesh) allocID() node.NodeID {
	return node.NodeID(m.nextID.Add(1))
}

func (m *Mesh) addNodeWithRole(role node.Role, level int, autoStart bool) *node.Node {
	id := m.allocID()
	n := node.NewNode(id, m.runtimeConfig())
	n.IP = node.SequentialIPv4ByOrdinal(uint64(id))
	n.SetRole(role)
	n.SetLevel(level)
	if role == node.RoleLeader {
		n.Leader = &node.LeaderState{}
	}
	mgrCfg := node.DefaultNodeManagerConfig()
	mgrCfg.SameLevelN = m.cfg.N
	mgrCfg.UpperLevelsY = m.cfg.Y
	n.Manager = node.NewNodeManager(n.PeerInfo(), mgrCfg)

	m.mu.Lock()
	if m.rootID != 0 && id != m.rootID {
		n.Conns.UpperLeader = m.rootID
		n.Manager.SetConnectionState(node.ConnStateJoining)
	} else {
		n.Manager.SetConnectionState(node.ConnStateSolo)
	}
	m.nodes[id] = n
	m.mu.Unlock()

	m.net.Register(n)
	if autoStart && m.cfg.NodeGoroutines {
		n.Start(&m.nodeWG)
	}
	return n
}

// NodeByID returns a node pointer if present.
func (m *Mesh) NodeByID(id node.NodeID) *node.Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodes[id]
}

// Bootstrap creates the root leader.
func (m *Mesh) Bootstrap() *node.Node {
	root := m.addNodeWithRole(node.RoleLeader, 0, true)
	m.mu.Lock()
	m.rootID = root.ID
	m.mu.Unlock()
	return root
}

// AddNode adds one base node.
func (m *Mesh) AddNode(ctx context.Context) (*node.Node, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	level := 1
	if m.RootID() == 0 {
		level = 0
	}
	return m.addNodeWithRole(node.RoleBase, level, true), nil
}

// AddNodeUnstarted adds one base node but does not start its goroutine.
// Useful when caller needs to complete registration/handler wiring first.
func (m *Mesh) AddNodeUnstarted(ctx context.Context) (*node.Node, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	level := 1
	if m.RootID() == 0 {
		level = 0
	}
	return m.addNodeWithRole(node.RoleBase, level, false), nil
}

// StartNode starts a node goroutine if NodeGoroutines is enabled.
func (m *Mesh) StartNode(n *node.Node) {
	if n == nil || !m.cfg.NodeGoroutines {
		return
	}
	n.Start(&m.nodeWG)
}

// RemoveNode removes one node from mesh/network and requests goroutine stop.
// Returns true if the node existed and was removed.
func (m *Mesh) RemoveNode(id node.NodeID) bool {
	m.mu.Lock()
	n, ok := m.nodes[id]
	if ok {
		delete(m.nodes, id)
		if m.rootID == id {
			m.rootID = 0
		}
	}
	m.mu.Unlock()
	if !ok || n == nil {
		return false
	}
	m.net.Deregister(id)
	n.Stop()
	return true
}

// SpawnNodes spawns count nodes.
// If async is true, creation is parallelized (bounded by SpawnParallelism).
func (m *Mesh) SpawnNodes(ctx context.Context, count int, async bool) error {
	if count < 0 {
		return errors.New("count must be >= 0")
	}
	if count == 0 {
		return nil
	}

	waitSpawnDelay := func() error {
		if m.cfg.SpawnDelay <= 0 {
			return nil
		}
		timer := time.NewTimer(m.cfg.SpawnDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}

	if !async {
		for i := 0; i < count; i++ {
			if err := waitSpawnDelay(); err != nil {
				return err
			}
			if _, err := m.AddNode(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	parallelism := m.cfg.SpawnParallelism
	if parallelism <= 0 {
		parallelism = runtime.GOMAXPROCS(0)
	}
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	for i := 0; i < count; i++ {
		if err := waitSpawnDelay(); err != nil {
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
			if _, err := m.AddNode(ctx); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}()
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

// RandomNode returns any random node except exclude.
func (m *Mesh) RandomNode(exclude node.NodeID) *node.Node {
	m.rngMu.Lock()
	defer m.rngMu.Unlock()
	return m.net.RandomNode(m.rng, exclude)
}

// RootID returns current root id (0 if not bootstrapped yet).
func (m *Mesh) RootID() node.NodeID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rootID
}

// NodesSnapshot returns a stable slice of current node pointers.
func (m *Mesh) NodesSnapshot() []*node.Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*node.Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	return out
}

// Stats returns a point-in-time aggregate.
func (m *Mesh) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var s Stats
	s.TotalNodes = len(m.nodes)
	s.Subnets = m.net.SubnetCount()
	for _, n := range m.nodes {
		switch n.Role {
		case node.RoleLeader:
			s.Leaders++
		case node.RoleSender:
			s.Senders++
		default:
			s.Base++
		}
		if n.IsRunning() {
			s.RunningNodes++
		}
	}
	return s
}

// Shutdown stops all node goroutines and waits for termination.
func (m *Mesh) Shutdown(timeout time.Duration) error {
	m.mu.RLock()
	nodes := make([]*node.Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		nodes = append(nodes, n)
	}
	m.mu.RUnlock()

	for _, n := range nodes {
		n.Stop()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.nodeWG.Wait()
	}()

	if timeout <= 0 {
		<-done
		return nil
	}

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return errors.New("shutdown timeout")
	}
}
