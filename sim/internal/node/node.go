package node

import (
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// RuntimeConfig controls goroutine/runtime behavior of a node.
type RuntimeConfig struct {
	TickPeriod time.Duration
	InboxSize  int

	EnableQDrift    bool
	QInitMin        float64
	QInitMax        float64
	QDriftMaxPerSec float64
	QUpdateInterval time.Duration
}

// DefaultRuntimeConfig returns safe defaults for scaffolding.
func DefaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		TickPeriod:      50 * time.Millisecond,
		InboxSize:       128,
		EnableQDrift:    true,
		QInitMin:        0.45,
		QInitMax:        0.85,
		QDriftMaxPerSec: 0.01, // max +-1% per second
		QUpdateInterval: time.Second,
	}
}

// Node is a single simulation actor.
// It exposes hooks (OnTick/OnMessage) that you can replace with real logic later.
type Node struct {
	ID      NodeID
	IP      string
	Level   int
	Role    Role
	Context NetContext
	Manager *NodeManager

	Profile NodeProfile
	State   SimState
	Metrics NodeMetrics
	Conns   NodeConns
	Leader  *LeaderState

	Inbox chan Message

	OnTick    func(*Node, time.Time)
	OnMessage func(*Node, Message)

	handlerMu sync.RWMutex

	runtime RuntimeConfig
	done    chan struct{}
	once    sync.Once
	running atomic.Bool
	ticks   atomic.Uint64

	qRng                 *rand.Rand
	qElapsed             time.Duration
	qFreezeUntilUnixNano atomic.Int64
}

// NewNode creates a node with no behavior logic attached.
func NewNode(id NodeID, cfg RuntimeConfig) *Node {
	if cfg.TickPeriod <= 0 {
		cfg.TickPeriod = 50 * time.Millisecond
	}
	if cfg.InboxSize <= 0 {
		cfg.InboxSize = 128
	}
	if cfg.QInitMin < 0 {
		cfg.QInitMin = 0
	}
	if cfg.QInitMax > 1 {
		cfg.QInitMax = 1
	}
	if cfg.QInitMax < cfg.QInitMin {
		cfg.QInitMax = cfg.QInitMin
	}
	if cfg.QDriftMaxPerSec < 0 {
		cfg.QDriftMaxPerSec = 0
	}
	if cfg.QUpdateInterval <= 0 {
		cfg.QUpdateInterval = time.Second
	}

	n := &Node{
		ID:      id,
		Role:    RoleBase,
		Context: ContextLAN,
		Inbox:   make(chan Message, cfg.InboxSize),
		runtime: cfg,
		done:    make(chan struct{}),
		qRng:    rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)*1009)),
	}
	n.SetOnTick(func(nn *Node, _ time.Time) {
		nn.updateQDrift()
	})
	n.SetOnMessage(func(nn *Node, msg Message) {
		switch msg.Type {
		case MsgUDPDatagram:
			dg, ok := msg.Payload.(UDPDatagram)
			if !ok {
				log.Printf("comp=node event=msg_rx_bad_payload node=%d msg_type=%s from=%d", nn.ID, msg.Type.String(), msg.From)
				return
			}
			log.Printf(
				"comp=node event=udp_rx node=%d from=%d seq=%d subnet=%d kind=%s trace=%s payload=%q",
				nn.ID, dg.From, dg.Seq, dg.SubnetID, dg.Kind, dg.TraceID, string(dg.Payload),
			)
		default:
			log.Printf("comp=node event=msg_rx node=%d msg_type=%s from=%d", nn.ID, msg.Type.String(), msg.From)
		}
	})
	n.initQ()
	return n
}

// Start launches the node goroutine.
func (n *Node) Start(wg *sync.WaitGroup) {
	if !n.running.CompareAndSwap(false, true) {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer n.running.Store(false)

		ticker := time.NewTicker(n.runtime.TickPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-n.done:
				return
			case msg := <-n.Inbox:
				n.handlerMu.RLock()
				onMessage := n.OnMessage
				n.handlerMu.RUnlock()
				onMessage(n, msg)
			case t := <-ticker.C:
				n.ticks.Add(1)
				n.handlerMu.RLock()
				onTick := n.OnTick
				n.handlerMu.RUnlock()
				onTick(n, t)
			}
		}
	}()
}

// SetOnMessage safely replaces the current message handler.
func (n *Node) SetOnMessage(fn func(*Node, Message)) {
	if fn == nil {
		fn = func(*Node, Message) {}
	}
	n.handlerMu.Lock()
	n.OnMessage = fn
	n.handlerMu.Unlock()
}

// SetOnTick safely replaces the current tick handler.
func (n *Node) SetOnTick(fn func(*Node, time.Time)) {
	if fn == nil {
		fn = func(*Node, time.Time) {}
	}
	n.handlerMu.Lock()
	n.OnTick = fn
	n.handlerMu.Unlock()
}

// Stop requests goroutine shutdown.
func (n *Node) Stop() {
	n.once.Do(func() {
		close(n.done)
	})
}

// IsRunning reports whether the node goroutine is active.
func (n *Node) IsRunning() bool { return n.running.Load() }

// TickCount returns how many periodic ticks have been processed by this node.
func (n *Node) TickCount() uint64 { return n.ticks.Load() }

func (n *Node) initQ() {
	q := n.runtime.QInitMin
	if n.runtime.QInitMax > n.runtime.QInitMin {
		q += n.qRng.Float64() * (n.runtime.QInitMax - n.runtime.QInitMin)
	}
	q = clamp01(q)
	n.Metrics.Q = q
	n.Metrics.QRaw = q
	n.Metrics.QSmooth = q
	n.Metrics.HR = q
}

func (n *Node) updateQDrift() {
	if !n.runtime.EnableQDrift || n.runtime.QDriftMaxPerSec <= 0 {
		return
	}
	if n.isQDriftFrozen(time.Now()) {
		return
	}
	n.qElapsed += n.runtime.TickPeriod
	step := n.runtime.QUpdateInterval
	if step <= 0 {
		step = time.Second
	}
	for n.qElapsed >= step {
		n.qElapsed -= step
		stepScale := step.Seconds()
		delta := (n.qRng.Float64()*2 - 1) * (n.runtime.QDriftMaxPerSec * stepScale)
		q := clamp01(n.Metrics.Q + delta)
		n.Metrics.Q = q
		n.Metrics.QRaw = q
		n.Metrics.QSmooth = q
		n.Metrics.HR = q
		if n.Manager != nil {
			n.Manager.UpdateSelfQ(q)
		}
	}
}

// FreezeQDriftFor pauses Q drift updates for a bounded duration.
func (n *Node) FreezeQDriftFor(d time.Duration) {
	if d <= 0 {
		return
	}
	until := time.Now().Add(d).UnixNano()
	for {
		cur := n.qFreezeUntilUnixNano.Load()
		if cur >= until {
			return
		}
		if n.qFreezeUntilUnixNano.CompareAndSwap(cur, until) {
			return
		}
	}
}

func (n *Node) isQDriftFrozen(now time.Time) bool {
	until := n.qFreezeUntilUnixNano.Load()
	if until <= 0 {
		return false
	}
	return now.UnixNano() < until
}

// SetRole updates both node runtime role and local manager role cache.
func (n *Node) SetRole(role Role) {
	n.Role = role
	if n.Manager != nil {
		n.Manager.UpdateSelfRole(role)
	}
}

// SetLevel updates both node runtime level and local manager level cache.
func (n *Node) SetLevel(level int) {
	n.Level = level
	if n.Manager != nil {
		n.Manager.UpdateSelfLevel(level)
	}
}

// PeerInfo returns the compact topology descriptor for this node.
func (n *Node) PeerInfo() PeerInfo {
	return PeerInfo{
		ID:    n.ID,
		IP:    n.IP,
		Q:     n.Metrics.Q,
		Role:  n.Role,
		Level: n.Level,
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
