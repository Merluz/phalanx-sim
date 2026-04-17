package node

// NodeID is the unique identifier of a simulated node.
type NodeID uint64

// Role is the runtime role of a node.
type Role uint8

const (
	RoleBase Role = iota
	RoleSender
	RoleLeader
)

func (r Role) String() string {
	switch r {
	case RoleBase:
		return "base"
	case RoleSender:
		return "sender"
	case RoleLeader:
		return "leader"
	default:
		return "unknown"
	}
}

// NetContext identifies the environment where a node operates.
type NetContext uint8

const (
	ContextLAN NetContext = iota
	ContextWAN
)

// ConnState captures the high-level connection phase of a node.
type ConnState uint8

const (
	ConnStateSolo ConnState = iota
	ConnStateJoining
	ConnStateConnected
	ConnStateTransitioning
	ConnStateDisconnected
)

func (s ConnState) String() string {
	switch s {
	case ConnStateSolo:
		return "solo"
	case ConnStateJoining:
		return "joining"
	case ConnStateConnected:
		return "connected"
	case ConnStateTransitioning:
		return "transitioning"
	case ConnStateDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// NodeProfile is a placeholder profile that can be used to drive synthetic behavior.
// This scaffold does not implement profile-driven logic yet.
type NodeProfile struct {
	StabilityTend    float64
	ResourceTend     float64
	ConnectivityTend float64
	Volatility       float64
}

// SimState is a placeholder mutable state for per-node simulation internals.
// Keep/extend as needed while implementing node behavior.
type SimState struct {
	PacketLoss      float64
	LinkJitterMs    float64
	LinkLatencyMs   float64
	CPU             float64
	Mem             float64
	IO              float64
	TempC           float64
	TimeSinceReboot float64
}

// NodeConns models the topology-facing relationships of a node.
type NodeConns struct {
	Successors   []NodeID
	UpperLeader  NodeID
	LowerLeaders []NodeID
	Random       []NodeID
}

// LeaderState holds leader-only data.
type LeaderState struct {
	SuccessorList []NodeID
}
