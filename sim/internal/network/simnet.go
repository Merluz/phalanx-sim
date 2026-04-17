package network

import (
	"math/rand"
	"sync"

	"phalanxsim2/internal/node"
)

// SimNetwork is an in-memory transport/registry scaffold.
type SimNetwork struct {
	mu         sync.RWMutex
	nodes      map[node.NodeID]*node.Node
	subnets    map[uint64]map[node.NodeID]struct{}
	subnetSize uint64
}

// New creates an empty network.
func New(subnetSize uint64) *SimNetwork {
	if subnetSize == 0 {
		subnetSize = 1 << 24
	}
	return &SimNetwork{
		nodes:      make(map[node.NodeID]*node.Node),
		subnets:    make(map[uint64]map[node.NodeID]struct{}),
		subnetSize: subnetSize,
	}
}

func (sn *SimNetwork) subnetKey(id node.NodeID) uint64 { return uint64(id) / sn.subnetSize }

// Register adds a node to the network registry.
func (sn *SimNetwork) Register(n *node.Node) {
	sn.mu.Lock()
	defer sn.mu.Unlock()

	sn.nodes[n.ID] = n
	key := sn.subnetKey(n.ID)
	if sn.subnets[key] == nil {
		sn.subnets[key] = make(map[node.NodeID]struct{})
	}
	sn.subnets[key][n.ID] = struct{}{}
}

// Deregister removes a node from the registry.
func (sn *SimNetwork) Deregister(id node.NodeID) {
	sn.mu.Lock()
	defer sn.mu.Unlock()

	delete(sn.nodes, id)
	key := sn.subnetKey(id)
	if bucket := sn.subnets[key]; bucket != nil {
		delete(bucket, id)
		if len(bucket) == 0 {
			delete(sn.subnets, key)
		}
	}
}

// Get returns a node by id or nil.
func (sn *SimNetwork) Get(id node.NodeID) *node.Node {
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	return sn.nodes[id]
}

// Send enqueues a message to the target inbox (non-blocking).
func (sn *SimNetwork) Send(to node.NodeID, msg node.Message) bool {
	sn.mu.RLock()
	target := sn.nodes[to]
	sn.mu.RUnlock()
	if target == nil {
		return false
	}
	select {
	case target.Inbox <- msg:
		return true
	default:
		return false
	}
}

// RandomNode returns a random node, excluding `exclude`.
func (sn *SimNetwork) RandomNode(rng *rand.Rand, exclude node.NodeID) *node.Node {
	sn.mu.RLock()
	defer sn.mu.RUnlock()

	candidates := make([]*node.Node, 0, len(sn.nodes))
	for id, n := range sn.nodes {
		if id == exclude {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) == 0 {
		return nil
	}
	return candidates[rng.Intn(len(candidates))]
}

// Len returns current node count.
func (sn *SimNetwork) Len() int {
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	return len(sn.nodes)
}

// SubnetCount returns active subnet count.
func (sn *SimNetwork) SubnetCount() int {
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	return len(sn.subnets)
}

