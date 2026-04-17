package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// WSControlType identifies the semantic control packet carried over WSFrame.
type WSControlType string

const (
	WSControlHello            WSControlType = "hello"
	WSControlHelloAck         WSControlType = "hello_ack"
	WSControlHelloRelay       WSControlType = "hello_relay"
	WSControlTransitionHint   WSControlType = "transition_hint"
	WSControlRotatePrepare    WSControlType = "leader_rotate_prepare"
	WSControlRotatePrepareAck WSControlType = "leader_rotate_prepare_ack"
	WSControlRotateCommit     WSControlType = "leader_rotate_commit"
	WSControlRotateCommitAck  WSControlType = "leader_rotate_commit_ack"
	WSControlRotateApplied    WSControlType = "leader_rotate_applied"
)

// WSPacket is implemented by all typed WS control packets.
type WSPacket interface {
	PacketType() WSControlType
	Validate() error
}

// PacketMeta carries common metadata for control packets.
type PacketMeta struct {
	Type    WSControlType `json:"type"`
	TraceID string        `json:"trace_id"`
	Epoch   uint64        `json:"epoch"`
	SentAt  time.Time     `json:"sent_at"`
}

// WSHelloPacket is the first handshake packet between two nodes.
// Essential fields are embedded in From: IP + Q + identity.
type WSHelloPacket struct {
	PacketMeta
	From            PeerInfo  `json:"from"`
	KnownLeader     PeerInfo  `json:"known_leader,omitempty"`
	ConnectionState ConnState `json:"connection_state"`
	SenderThreshold float64   `json:"sender_threshold"`
	Capabilities    []string  `json:"capabilities,omitempty"`
}

func (p WSHelloPacket) PacketType() WSControlType { return WSControlHello }

func NewWSHelloPacket(traceID string, epoch uint64, from PeerInfo, senderThreshold float64) WSHelloPacket {
	return WSHelloPacket{
		PacketMeta: PacketMeta{
			Type:    WSControlHello,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		From:            from,
		ConnectionState: ConnStateJoining,
		SenderThreshold: senderThreshold,
	}
}

func (p WSHelloPacket) Validate() error {
	if p.From.ID == 0 {
		return errors.New("hello.from.id must be set")
	}
	if p.From.IP == "" {
		return errors.New("hello.from.ip must be set")
	}
	if p.From.Q < 0 || p.From.Q > 1 {
		return errors.New("hello.from.q must be in [0,1]")
	}
	if p.SenderThreshold < 0 || p.SenderThreshold > 1 {
		return errors.New("hello.sender_threshold must be in [0,1]")
	}
	return nil
}

// WSHelloAckPacket is returned by the accepting leader (directly or via relay)
// and carries initial placement data + failover cache.
type WSHelloAckPacket struct {
	PacketMeta
	Leader           PeerInfo         `json:"leader"`
	AcceptedBy       PeerInfo         `json:"accepted_by"`
	AssignedRole     Role             `json:"assigned_role"`
	AssignedLevel    int              `json:"assigned_level"`
	TotalLevels      int              `json:"total_levels"`
	TopologyRevision uint64           `json:"topology_revision"`
	SameLevel        []PeerInfo       `json:"same_level,omitempty"`
	UpperLevels      []UpperLevelView `json:"upper_levels,omitempty"`
	Failover         []PeerInfo       `json:"failover,omitempty"`
}

func (p WSHelloAckPacket) PacketType() WSControlType { return WSControlHelloAck }

func NewWSHelloAckPacket(traceID string, epoch uint64, leader PeerInfo) WSHelloAckPacket {
	return WSHelloAckPacket{
		PacketMeta: PacketMeta{
			Type:    WSControlHelloAck,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		Leader:      leader,
		TotalLevels: 1,
	}
}

func (p WSHelloAckPacket) Validate() error {
	if p.Leader.ID == 0 {
		return errors.New("hello_ack.leader.id must be set")
	}
	if p.Leader.IP == "" {
		return errors.New("hello_ack.leader.ip must be set")
	}
	if p.AssignedRole != RoleBase && p.AssignedRole != RoleSender && p.AssignedRole != RoleLeader {
		return errors.New("hello_ack.assigned_role is invalid")
	}
	if p.AssignedLevel < 0 {
		return errors.New("hello_ack.assigned_level must be >= 0")
	}
	if p.TotalLevels <= 0 {
		return errors.New("hello_ack.total_levels must be > 0")
	}
	return nil
}

// WSHelloRelayPacket forwards HELLO to another hierarchy level while preserving
// origin identity and quick routing context.
type WSHelloRelayPacket struct {
	PacketMeta
	Origin      PeerInfo `json:"origin"`
	Via         PeerInfo `json:"via"`
	TargetLevel int      `json:"target_level"`
	Direction   string   `json:"direction"` // up|down
	Hop         int      `json:"hop"`
	MaxHop      int      `json:"max_hop"`
	Reason      string   `json:"reason,omitempty"`
}

func (p WSHelloRelayPacket) PacketType() WSControlType { return WSControlHelloRelay }

func NewWSHelloRelayPacket(traceID string, epoch uint64, origin, via PeerInfo, direction string) WSHelloRelayPacket {
	return WSHelloRelayPacket{
		PacketMeta: PacketMeta{
			Type:    WSControlHelloRelay,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		Origin:    origin,
		Via:       via,
		Direction: direction,
		MaxHop:    16,
	}
}

func (p WSHelloRelayPacket) Validate() error {
	if p.Origin.ID == 0 {
		return errors.New("hello_relay.origin.id must be set")
	}
	if p.Origin.IP == "" {
		return errors.New("hello_relay.origin.ip must be set")
	}
	if p.Origin.Q < 0 || p.Origin.Q > 1 {
		return errors.New("hello_relay.origin.q must be in [0,1]")
	}
	if p.Via.ID == 0 {
		return errors.New("hello_relay.via.id must be set")
	}
	if p.Direction != "up" && p.Direction != "down" {
		return errors.New("hello_relay.direction must be up or down")
	}
	if p.Hop < 0 || p.MaxHop < 0 || p.Hop > p.MaxHop {
		return errors.New("hello_relay hop metadata is invalid")
	}
	return nil
}

// WSTransitionHintPacket is a forward-compatible packet used during
// make-before-break connection transitions (kept for next phases).
type WSTransitionHintPacket struct {
	PacketMeta
	Node            PeerInfo `json:"node"`
	NewTarget       PeerInfo `json:"new_target"`
	KeepOldUntilAck bool     `json:"keep_old_until_ack"`
	Reason          string   `json:"reason,omitempty"`
}

func (p WSTransitionHintPacket) PacketType() WSControlType { return WSControlTransitionHint }

func NewWSTransitionHintPacket(traceID string, epoch uint64, nodeInfo, newTarget PeerInfo) WSTransitionHintPacket {
	return WSTransitionHintPacket{
		PacketMeta: PacketMeta{
			Type:    WSControlTransitionHint,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		Node:      nodeInfo,
		NewTarget: newTarget,
	}
}

func (p WSTransitionHintPacket) Validate() error {
	if p.Node.ID == 0 || p.NewTarget.ID == 0 {
		return errors.New("transition_hint node and new_target must be set")
	}
	if p.Node.IP == "" || p.NewTarget.IP == "" {
		return errors.New("transition_hint node and new_target must have ip")
	}
	return nil
}

// WSLeaderRotatePreparePacket starts a same-level leader rotation proposal.
type WSLeaderRotatePreparePacket struct {
	PacketMeta
	ProposalID      string   `json:"proposal_id"`
	Level           int      `json:"level"`
	OldLeader       PeerInfo `json:"old_leader"`
	NewLeader       PeerInfo `json:"new_leader"`
	CurrentRevision uint64   `json:"current_revision"`
	MeanQ           float64  `json:"mean_q"`
	OldLeaderQ      float64  `json:"old_leader_q"`
	CandidateQ      float64  `json:"candidate_q"`
	TriggerDropPct  float64  `json:"trigger_drop_pct"`
	TriggerMinDelta float64  `json:"trigger_min_delta"`
}

func (p WSLeaderRotatePreparePacket) PacketType() WSControlType { return WSControlRotatePrepare }

func NewWSLeaderRotatePreparePacket(traceID string, epoch uint64, level int, oldLeader, newLeader PeerInfo) WSLeaderRotatePreparePacket {
	return WSLeaderRotatePreparePacket{
		PacketMeta: PacketMeta{
			Type:    WSControlRotatePrepare,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		ProposalID: traceID,
		Level:      level,
		OldLeader:  oldLeader,
		NewLeader:  newLeader,
	}
}

func (p WSLeaderRotatePreparePacket) Validate() error {
	if p.ProposalID == "" {
		return errors.New("rotate_prepare.proposal_id must be set")
	}
	if p.Level < 0 {
		return errors.New("rotate_prepare.level must be >= 0")
	}
	if p.OldLeader.ID == 0 || p.NewLeader.ID == 0 {
		return errors.New("rotate_prepare old/new leader must be set")
	}
	if p.OldLeader.IP == "" || p.NewLeader.IP == "" {
		return errors.New("rotate_prepare old/new leader ip must be set")
	}
	if p.OldLeaderQ < 0 || p.OldLeaderQ > 1 || p.CandidateQ < 0 || p.CandidateQ > 1 || p.MeanQ < 0 || p.MeanQ > 1 {
		return errors.New("rotate_prepare q fields must be in [0,1]")
	}
	if p.TriggerDropPct < 0 || p.TriggerDropPct >= 1 {
		return errors.New("rotate_prepare.trigger_drop_pct must be in [0,1)")
	}
	if p.TriggerMinDelta < 0 || p.TriggerMinDelta > 1 {
		return errors.New("rotate_prepare.trigger_min_delta must be in [0,1]")
	}
	return nil
}

// WSLeaderRotatePrepareAckPacket is emitted by nodes after PREPARE evaluation.
type WSLeaderRotatePrepareAckPacket struct {
	PacketMeta
	ProposalID    string   `json:"proposal_id"`
	Level         int      `json:"level"`
	FromPeer      PeerInfo `json:"from_peer"`
	Accept        bool     `json:"accept"`
	KnownRevision uint64   `json:"known_revision"`
	Reason        string   `json:"reason,omitempty"`
}

func (p WSLeaderRotatePrepareAckPacket) PacketType() WSControlType { return WSControlRotatePrepareAck }

func NewWSLeaderRotatePrepareAckPacket(traceID string, epoch uint64, proposalID string, level int, fromPeer PeerInfo, accept bool) WSLeaderRotatePrepareAckPacket {
	return WSLeaderRotatePrepareAckPacket{
		PacketMeta: PacketMeta{
			Type:    WSControlRotatePrepareAck,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		ProposalID: proposalID,
		Level:      level,
		FromPeer:   fromPeer,
		Accept:     accept,
	}
}

func (p WSLeaderRotatePrepareAckPacket) Validate() error {
	if p.ProposalID == "" {
		return errors.New("rotate_prepare_ack.proposal_id must be set")
	}
	if p.Level < 0 {
		return errors.New("rotate_prepare_ack.level must be >= 0")
	}
	if p.FromPeer.ID == 0 || p.FromPeer.IP == "" {
		return errors.New("rotate_prepare_ack.from_peer must be set")
	}
	return nil
}

// WSLeaderRotateCommitPacket commits the selected leader replacement.
type WSLeaderRotateCommitPacket struct {
	PacketMeta
	ProposalID     string   `json:"proposal_id"`
	Level          int      `json:"level"`
	OldLeader      PeerInfo `json:"old_leader"`
	NewLeader      PeerInfo `json:"new_leader"`
	PrevRevision   uint64   `json:"prev_revision"`
	TargetRevision uint64   `json:"target_revision"`
}

func (p WSLeaderRotateCommitPacket) PacketType() WSControlType { return WSControlRotateCommit }

func NewWSLeaderRotateCommitPacket(traceID string, epoch uint64, level int, oldLeader, newLeader PeerInfo) WSLeaderRotateCommitPacket {
	return WSLeaderRotateCommitPacket{
		PacketMeta: PacketMeta{
			Type:    WSControlRotateCommit,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		ProposalID: traceID,
		Level:      level,
		OldLeader:  oldLeader,
		NewLeader:  newLeader,
	}
}

func (p WSLeaderRotateCommitPacket) Validate() error {
	if p.ProposalID == "" {
		return errors.New("rotate_commit.proposal_id must be set")
	}
	if p.Level < 0 {
		return errors.New("rotate_commit.level must be >= 0")
	}
	if p.OldLeader.ID == 0 || p.NewLeader.ID == 0 {
		return errors.New("rotate_commit old/new leader must be set")
	}
	if p.OldLeader.IP == "" || p.NewLeader.IP == "" {
		return errors.New("rotate_commit old/new leader ip must be set")
	}
	if p.TargetRevision > 0 && p.PrevRevision > p.TargetRevision {
		return errors.New("rotate_commit prev_revision must be <= target_revision")
	}
	return nil
}

// WSLeaderRotateCommitAckPacket is emitted by nodes after COMMIT evaluation.
type WSLeaderRotateCommitAckPacket struct {
	PacketMeta
	ProposalID    string   `json:"proposal_id"`
	Level         int      `json:"level"`
	FromPeer      PeerInfo `json:"from_peer"`
	Accept        bool     `json:"accept"`
	KnownRevision uint64   `json:"known_revision"`
	Reason        string   `json:"reason,omitempty"`
}

func (p WSLeaderRotateCommitAckPacket) PacketType() WSControlType { return WSControlRotateCommitAck }

func NewWSLeaderRotateCommitAckPacket(traceID string, epoch uint64, proposalID string, level int, fromPeer PeerInfo, accept bool) WSLeaderRotateCommitAckPacket {
	return WSLeaderRotateCommitAckPacket{
		PacketMeta: PacketMeta{
			Type:    WSControlRotateCommitAck,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		ProposalID: proposalID,
		Level:      level,
		FromPeer:   fromPeer,
		Accept:     accept,
	}
}

func (p WSLeaderRotateCommitAckPacket) Validate() error {
	if p.ProposalID == "" {
		return errors.New("rotate_commit_ack.proposal_id must be set")
	}
	if p.Level < 0 {
		return errors.New("rotate_commit_ack.level must be >= 0")
	}
	if p.FromPeer.ID == 0 || p.FromPeer.IP == "" {
		return errors.New("rotate_commit_ack.from_peer must be set")
	}
	return nil
}

// WSLeaderRotateAppliedPacket confirms the rotation has been applied.
type WSLeaderRotateAppliedPacket struct {
	PacketMeta
	Level         int      `json:"level"`
	OldLeader     PeerInfo `json:"old_leader"`
	NewLeader     PeerInfo `json:"new_leader"`
	AppliedBy     PeerInfo `json:"applied_by"`
	FinalRevision uint64   `json:"final_revision"`
}

func (p WSLeaderRotateAppliedPacket) PacketType() WSControlType { return WSControlRotateApplied }

func NewWSLeaderRotateAppliedPacket(traceID string, epoch uint64, level int, oldLeader, newLeader, appliedBy PeerInfo) WSLeaderRotateAppliedPacket {
	return WSLeaderRotateAppliedPacket{
		PacketMeta: PacketMeta{
			Type:    WSControlRotateApplied,
			TraceID: traceID,
			Epoch:   epoch,
			SentAt:  time.Now(),
		},
		Level:     level,
		OldLeader: oldLeader,
		NewLeader: newLeader,
		AppliedBy: appliedBy,
	}
}

func (p WSLeaderRotateAppliedPacket) Validate() error {
	if p.Level < 0 {
		return errors.New("rotate_applied.level must be >= 0")
	}
	if p.OldLeader.ID == 0 || p.NewLeader.ID == 0 || p.AppliedBy.ID == 0 {
		return errors.New("rotate_applied old/new/applied_by must be set")
	}
	if p.OldLeader.IP == "" || p.NewLeader.IP == "" || p.AppliedBy.IP == "" {
		return errors.New("rotate_applied old/new/applied_by ip must be set")
	}
	return nil
}

// EncodeWSPacket serializes a typed packet into WSFrame payload bytes.
func EncodeWSPacket(pkt WSPacket) ([]byte, error) {
	if pkt == nil {
		return nil, errors.New("packet is nil")
	}
	if err := pkt.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(pkt)
}

// DecodeWSPacketType peeks only packet type without fully decoding payload.
func DecodeWSPacketType(payload []byte) (WSControlType, error) {
	if len(payload) == 0 {
		return "", errors.New("empty ws payload")
	}
	var h struct {
		Type WSControlType `json:"type"`
	}
	if err := json.Unmarshal(payload, &h); err != nil {
		return "", err
	}
	if h.Type == "" {
		return "", errors.New("missing packet type")
	}
	return h.Type, nil
}

// DecodeWSHelloPacket decodes and validates a HELLO packet payload.
func DecodeWSHelloPacket(payload []byte) (WSHelloPacket, error) {
	var p WSHelloPacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSHelloPacket{}, err
	}
	if p.Type != WSControlHello {
		return WSHelloPacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSHelloPacket{}, err
	}
	return p, nil
}

// DecodeWSHelloAckPacket decodes and validates a HELLO_ACK packet payload.
func DecodeWSHelloAckPacket(payload []byte) (WSHelloAckPacket, error) {
	var p WSHelloAckPacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSHelloAckPacket{}, err
	}
	if p.Type != WSControlHelloAck {
		return WSHelloAckPacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSHelloAckPacket{}, err
	}
	return p, nil
}

// DecodeWSHelloRelayPacket decodes and validates a HELLO_RELAY packet payload.
func DecodeWSHelloRelayPacket(payload []byte) (WSHelloRelayPacket, error) {
	var p WSHelloRelayPacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSHelloRelayPacket{}, err
	}
	if p.Type != WSControlHelloRelay {
		return WSHelloRelayPacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSHelloRelayPacket{}, err
	}
	return p, nil
}

// DecodeWSTransitionHintPacket decodes and validates a TRANSITION_HINT packet payload.
func DecodeWSTransitionHintPacket(payload []byte) (WSTransitionHintPacket, error) {
	var p WSTransitionHintPacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSTransitionHintPacket{}, err
	}
	if p.Type != WSControlTransitionHint {
		return WSTransitionHintPacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSTransitionHintPacket{}, err
	}
	return p, nil
}

// DecodeWSLeaderRotatePreparePacket decodes and validates a LEADER_ROTATE_PREPARE payload.
func DecodeWSLeaderRotatePreparePacket(payload []byte) (WSLeaderRotatePreparePacket, error) {
	var p WSLeaderRotatePreparePacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSLeaderRotatePreparePacket{}, err
	}
	if p.Type != WSControlRotatePrepare {
		return WSLeaderRotatePreparePacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSLeaderRotatePreparePacket{}, err
	}
	return p, nil
}

// DecodeWSLeaderRotatePrepareAckPacket decodes and validates a LEADER_ROTATE_PREPARE_ACK payload.
func DecodeWSLeaderRotatePrepareAckPacket(payload []byte) (WSLeaderRotatePrepareAckPacket, error) {
	var p WSLeaderRotatePrepareAckPacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSLeaderRotatePrepareAckPacket{}, err
	}
	if p.Type != WSControlRotatePrepareAck {
		return WSLeaderRotatePrepareAckPacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSLeaderRotatePrepareAckPacket{}, err
	}
	return p, nil
}

// DecodeWSLeaderRotateCommitPacket decodes and validates a LEADER_ROTATE_COMMIT payload.
func DecodeWSLeaderRotateCommitPacket(payload []byte) (WSLeaderRotateCommitPacket, error) {
	var p WSLeaderRotateCommitPacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSLeaderRotateCommitPacket{}, err
	}
	if p.Type != WSControlRotateCommit {
		return WSLeaderRotateCommitPacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSLeaderRotateCommitPacket{}, err
	}
	return p, nil
}

// DecodeWSLeaderRotateCommitAckPacket decodes and validates a LEADER_ROTATE_COMMIT_ACK payload.
func DecodeWSLeaderRotateCommitAckPacket(payload []byte) (WSLeaderRotateCommitAckPacket, error) {
	var p WSLeaderRotateCommitAckPacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSLeaderRotateCommitAckPacket{}, err
	}
	if p.Type != WSControlRotateCommitAck {
		return WSLeaderRotateCommitAckPacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSLeaderRotateCommitAckPacket{}, err
	}
	return p, nil
}

// DecodeWSLeaderRotateAppliedPacket decodes and validates a LEADER_ROTATE_APPLIED payload.
func DecodeWSLeaderRotateAppliedPacket(payload []byte) (WSLeaderRotateAppliedPacket, error) {
	var p WSLeaderRotateAppliedPacket
	if err := json.Unmarshal(payload, &p); err != nil {
		return WSLeaderRotateAppliedPacket{}, err
	}
	if p.Type != WSControlRotateApplied {
		return WSLeaderRotateAppliedPacket{}, fmt.Errorf("unexpected type %q", p.Type)
	}
	if err := p.Validate(); err != nil {
		return WSLeaderRotateAppliedPacket{}, err
	}
	return p, nil
}
