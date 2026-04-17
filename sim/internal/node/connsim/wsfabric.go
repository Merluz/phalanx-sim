package connsim

import (
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"phalanxsim2/internal/node"
)

// WSConfig controls in-memory WS simulation behavior.
type WSConfig struct {
	MinDelay time.Duration
	MaxDelay time.Duration
	DropRate float64
}

// DefaultWSConfig returns safe defaults for WS control-plane simulation.
func DefaultWSConfig() WSConfig {
	return WSConfig{
		MinDelay: 1 * time.Millisecond,
		MaxDelay: 4 * time.Millisecond,
		DropRate: 0,
	}
}

type wsEndpoint struct {
	n *node.Node
}

type wsLinkKey struct {
	a node.NodeID
	b node.NodeID
}

type wsLink struct {
	ConnID   uint64
	A        node.NodeID
	B        node.NodeID
	Created  time.Time
	LastUsed time.Time
}

// WSFabric simulates bidirectional WS connections over in-memory inboxes.
type WSFabric struct {
	cfg WSConfig

	rng   *rand.Rand
	rngMu sync.Mutex

	mu    sync.RWMutex
	eps   map[node.NodeID]wsEndpoint
	links map[wsLinkKey]wsLink

	connSeq atomic.Uint64
}

// NewWSFabric builds a new WS simulation fabric.
func NewWSFabric(cfg WSConfig, rng *rand.Rand) *WSFabric {
	if cfg.MinDelay < 0 {
		cfg.MinDelay = 0
	}
	if cfg.MaxDelay < cfg.MinDelay {
		cfg.MaxDelay = cfg.MinDelay
	}
	if cfg.DropRate < 0 {
		cfg.DropRate = 0
	}
	if cfg.DropRate > 1 {
		cfg.DropRate = 1
	}
	return &WSFabric{
		cfg:   cfg,
		rng:   rng,
		eps:   make(map[node.NodeID]wsEndpoint),
		links: make(map[wsLinkKey]wsLink),
	}
}

// RegisterNode adds a node endpoint for WS delivery.
func (f *WSFabric) RegisterNode(n *node.Node) {
	f.mu.Lock()
	f.eps[n.ID] = wsEndpoint{n: n}
	f.mu.Unlock()
	log.Printf("comp=connsim layer=ws event=register node=%d", n.ID)
}

// DeregisterNode removes a node endpoint and its links.
func (f *WSFabric) DeregisterNode(id node.NodeID) {
	f.mu.Lock()
	delete(f.eps, id)
	for k := range f.links {
		if k.a == id || k.b == id {
			delete(f.links, k)
		}
	}
	f.mu.Unlock()
	log.Printf("comp=connsim layer=ws event=deregister node=%d", id)
}

// Connect creates or reuses a bidirectional WS link between two nodes.
func (f *WSFabric) Connect(a, b node.NodeID, traceID string) (uint64, bool) {
	if a == 0 || b == 0 || a == b {
		return 0, false
	}
	key := wsPairKey(a, b)

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.eps[a]; !ok {
		log.Printf("comp=connsim layer=ws event=connect_drop reason=unknown_endpoint node=%d peer=%d trace=%s", a, b, traceID)
		return 0, false
	}
	if _, ok := f.eps[b]; !ok {
		log.Printf("comp=connsim layer=ws event=connect_drop reason=unknown_endpoint node=%d peer=%d trace=%s", b, a, traceID)
		return 0, false
	}

	if link, exists := f.links[key]; exists {
		link.LastUsed = time.Now()
		f.links[key] = link
		log.Printf("comp=connsim layer=ws event=connect_reuse conn=%d a=%d b=%d trace=%s", link.ConnID, a, b, traceID)
		return link.ConnID, true
	}

	now := time.Now()
	link := wsLink{
		ConnID:   f.connSeq.Add(1),
		A:        key.a,
		B:        key.b,
		Created:  now,
		LastUsed: now,
	}
	f.links[key] = link
	log.Printf("comp=connsim layer=ws event=connect_ok conn=%d a=%d b=%d trace=%s", link.ConnID, a, b, traceID)
	return link.ConnID, true
}

// SendFrame sends one WS frame through an existing WS connection.
func (f *WSFabric) SendFrame(from, to node.NodeID, kind string, payload []byte, traceID string) bool {
	now := time.Now()
	key := wsPairKey(from, to)

	f.mu.Lock()
	link, hasLink := f.links[key]
	src, hasSrc := f.eps[from]
	dst, hasDst := f.eps[to]
	if hasLink {
		link.LastUsed = now
		f.links[key] = link
	}
	f.mu.Unlock()

	if !hasSrc || !hasDst {
		log.Printf("comp=connsim layer=ws event=tx_drop reason=unknown_endpoint from=%d to=%d kind=%s trace=%s", from, to, kind, traceID)
		return false
	}
	if !hasLink {
		log.Printf("comp=connsim layer=ws event=tx_drop reason=no_link from=%d to=%d kind=%s trace=%s", from, to, kind, traceID)
		return false
	}
	if f.shouldDrop() {
		log.Printf("comp=connsim layer=ws event=tx_drop reason=simulated_loss conn=%d from=%d to=%d kind=%s trace=%s", link.ConnID, from, to, kind, traceID)
		return false
	}

	delay := f.sampleDelay()
	frame := node.WSFrame{
		ConnID:  link.ConnID,
		TraceID: traceID,
		Kind:    kind,
		From:    from,
		To:      to,
		Payload: append([]byte(nil), payload...),
		SentAt:  now,
	}
	log.Printf("comp=connsim layer=ws event=tx_schedule conn=%d from=%d to=%d delay_ms=%d kind=%s trace=%s", link.ConnID, from, to, delay.Milliseconds(), kind, traceID)
	f.scheduleFrame(dst.n, src.n.ID, frame, delay)
	return true
}

func (f *WSFabric) scheduleFrame(target *node.Node, from node.NodeID, frame node.WSFrame, delay time.Duration) {
	time.AfterFunc(delay, func() {
		msg := node.Message{
			Type:    node.MsgWSFrame,
			From:    from,
			Payload: frame,
		}
		select {
		case target.Inbox <- msg:
			log.Printf("comp=connsim layer=ws event=rx_enqueue_ok conn=%d from=%d to=%d kind=%s trace=%s", frame.ConnID, from, target.ID, frame.Kind, frame.TraceID)
		default:
			log.Printf("comp=connsim layer=ws event=rx_enqueue_drop reason=inbox_full conn=%d from=%d to=%d kind=%s trace=%s", frame.ConnID, from, target.ID, frame.Kind, frame.TraceID)
		}
	})
}

func (f *WSFabric) sampleDelay() time.Duration {
	if f.cfg.MaxDelay <= f.cfg.MinDelay {
		return f.cfg.MinDelay
	}
	f.rngMu.Lock()
	delta := f.rng.Int63n(int64(f.cfg.MaxDelay-f.cfg.MinDelay) + 1)
	f.rngMu.Unlock()
	return f.cfg.MinDelay + time.Duration(delta)
}

func (f *WSFabric) shouldDrop() bool {
	if f.cfg.DropRate <= 0 {
		return false
	}
	f.rngMu.Lock()
	v := f.rng.Float64() < f.cfg.DropRate
	f.rngMu.Unlock()
	return v
}

func wsPairKey(a, b node.NodeID) wsLinkKey {
	if a < b {
		return wsLinkKey{a: a, b: b}
	}
	return wsLinkKey{a: b, b: a}
}
