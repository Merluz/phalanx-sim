package node

import "time"

// MsgType identifies message kind.
type MsgType uint8

const (
	MsgHeartbeat MsgType = iota
	MsgJoinRequest
	MsgJoinAccept
	MsgRankUpdate
	MsgFailureNotify
	MsgUDPDatagram
	MsgWSFrame
	MsgCustom
)

func (m MsgType) String() string {
	switch m {
	case MsgHeartbeat:
		return "heartbeat"
	case MsgJoinRequest:
		return "join_request"
	case MsgJoinAccept:
		return "join_accept"
	case MsgRankUpdate:
		return "rank_update"
	case MsgFailureNotify:
		return "failure_notify"
	case MsgUDPDatagram:
		return "udp_datagram"
	case MsgWSFrame:
		return "ws_frame"
	case MsgCustom:
		return "custom"
	default:
		return "unknown"
	}
}

// Message is the base envelope for in-simulation communication.
type Message struct {
	Type    MsgType
	From    NodeID
	Payload any
}

// UDPDatagram is a simulated UDP broadcast payload delivered via MsgUDPDatagram.
type UDPDatagram struct {
	Seq      uint64
	TraceID  string
	Kind     string
	From     NodeID
	To       NodeID
	SubnetID uint64
	Payload  []byte
	SentAt   time.Time
}

// WSFrame is a placeholder for the next step (virtual WS simulation).
// Payload is expected to carry JSON-encoded WSPacket bytes.
type WSFrame struct {
	ConnID  uint64
	TraceID string
	Kind    string
	From    NodeID
	To      NodeID
	Payload []byte
	SentAt  time.Time
}
