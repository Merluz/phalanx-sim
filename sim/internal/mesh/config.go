package mesh

import "time"

// Config holds scaffold-level settings.
type Config struct {
	// Topology placeholders (kept for compatibility with PHALANX docs).
	N int
	Y int

	// Runtime settings.
	TickPeriod       time.Duration
	InboxSize        int
	SubnetSize       uint64
	NodeGoroutines   bool
	AsyncSpawn       bool
	SpawnParallelism int
	// SpawnDelay is the wait applied before each node spawn operation.
	// Total spawn duration is approximately count * SpawnDelay.
	SpawnDelay time.Duration

	// Q dynamics.
	QDriftMaxPerSec float64
	QUpdateInterval time.Duration
}

// DefaultConfig returns conservative defaults suitable for local experimentation.
func DefaultConfig() Config {
	return Config{
		N:                5,
		Y:                3,
		TickPeriod:       50 * time.Millisecond,
		InboxSize:        128,
		SubnetSize:       1 << 24,
		NodeGoroutines:   true,
		AsyncSpawn:       false,
		SpawnParallelism: 0, // 0 => use GOMAXPROCS
		SpawnDelay:       0,
		QDriftMaxPerSec:  0.01,
		QUpdateInterval:  time.Second,
	}
}
