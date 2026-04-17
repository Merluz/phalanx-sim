package hierarchy

import (
	"sort"
	"sync"
	"sync/atomic"

	"phalanxsim2/internal/node"
)

// LevelState is the authoritative per-level ranking view.
type LevelState struct {
	Level      int             `json:"level"`
	Leader     node.PeerInfo   `json:"leader"`
	Candidates []node.PeerInfo `json:"candidates"`
	Overflow   []node.PeerInfo `json:"overflow"`
}

// Snapshot is a stable hierarchy view for readers.
type Snapshot struct {
	Revision    uint64       `json:"revision"`
	TotalLevels int          `json:"total_levels"`
	MaxLevel    int          `json:"max_level"`
	Levels      []LevelState `json:"levels"`
}

// State stores the authoritative hierarchy model for the simulation.
type State struct {
	n int

	revision atomic.Uint64

	mu     sync.RWMutex
	levels map[int]LevelState
}

// NewState creates a hierarchy state with capacity N for same-level candidates.
func NewState(n int) *State {
	if n <= 0 {
		n = 5
	}
	return &State{
		n:      n,
		levels: make(map[int]LevelState),
	}
}

// RebuildFromPeers rebuilds hierarchy ranking from current known peers.
// Level convention is root at level 0, then +1 towards periphery.
func (s *State) RebuildFromPeers(peers []node.PeerInfo) Snapshot {
	byLevel := make(map[int][]node.PeerInfo)
	for _, p := range peers {
		if p.ID == 0 || p.Level < 0 {
			continue
		}
		byLevel[p.Level] = append(byLevel[p.Level], p)
	}

	next := make(map[int]LevelState, len(byLevel))
	maxLevel := 0
	for level, members := range byLevel {
		if level > maxLevel {
			maxLevel = level
		}
		sort.Slice(members, func(i, j int) bool {
			return betterPeer(members[i], members[j])
		})
		st := LevelState{Level: level}
		if len(members) > 0 {
			st.Leader = members[0]
		}
		if len(members) > 1 {
			end := 1 + s.n
			if end > len(members) {
				end = len(members)
			}
			st.Candidates = append([]node.PeerInfo(nil), members[1:end]...)
			if end < len(members) {
				st.Overflow = append([]node.PeerInfo(nil), members[end:]...)
			}
		}
		next[level] = st
	}

	s.mu.Lock()
	s.levels = next
	s.mu.Unlock()
	s.revision.Add(1)

	return s.Snapshot()
}

// Snapshot returns a stable copy of the hierarchy state.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := Snapshot{
		Revision: s.revision.Load(),
		Levels:   make([]LevelState, 0, len(s.levels)),
	}
	for _, st := range s.levels {
		cp := LevelState{
			Level:      st.Level,
			Leader:     st.Leader,
			Candidates: append([]node.PeerInfo(nil), st.Candidates...),
			Overflow:   append([]node.PeerInfo(nil), st.Overflow...),
		}
		out.Levels = append(out.Levels, cp)
		if cp.Level > out.MaxLevel {
			out.MaxLevel = cp.Level
		}
	}
	sort.Slice(out.Levels, func(i, j int) bool {
		return out.Levels[i].Level < out.Levels[j].Level
	})
	if len(out.Levels) == 0 {
		out.TotalLevels = 0
		out.MaxLevel = 0
	} else {
		out.TotalLevels = out.MaxLevel + 1
	}
	return out
}

func betterPeer(a, b node.PeerInfo) bool {
	if a.Q == b.Q {
		return a.ID < b.ID
	}
	return a.Q > b.Q
}
