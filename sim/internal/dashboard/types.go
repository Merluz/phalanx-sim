package dashboard

// StartConfig contains all runtime knobs exposed by the web dashboard.
// All durations are expressed in milliseconds for simple JSON serialization.
type StartConfig struct {
	N                 int    `json:"n"`
	Y                 int    `json:"y"`
	Nodes             int    `json:"nodes"`
	TickMS            int    `json:"tick_ms"`
	InboxSize         int    `json:"inbox_size"`
	SubnetSize        uint64 `json:"subnet_size"`
	NodeGoroutines    bool   `json:"node_goroutines"`
	AsyncSpawn        bool   `json:"async_spawn"`
	SpawnParallelism  int    `json:"spawn_parallelism"`
	SpawnDelayMS      int    `json:"spawn_delay_ms"`
	ShutdownTimeoutMS int    `json:"shutdown_timeout_ms"`
	Seed              int64  `json:"seed"`

	UDPDemo       bool    `json:"udp_demo"`
	UDPIntervalMS int     `json:"udp_interval_ms"`
	UDPSender     string  `json:"udp_sender"` // root|random
	UDPKind       string  `json:"udp_kind"`
	UDPPayload    string  `json:"udp_payload"`
	UDPSubnets    int     `json:"udp_subnets"`
	UDPDelayMinMS int     `json:"udp_delay_min_ms"`
	UDPDelayMaxMS int     `json:"udp_delay_max_ms"`
	UDPDropRate   float64 `json:"udp_drop_rate"`
	// Random-mode discovery controls (anti O(N^2) storm).
	RandomDiscoverFanout          int `json:"random_discover_fanout"`
	RandomDiscoverBackoffBaseMS   int `json:"random_discover_backoff_base_ms"`
	RandomDiscoverBackoffMaxMS    int `json:"random_discover_backoff_max_ms"`
	RandomDiscoverBackoffJitterMS int `json:"random_discover_backoff_jitter_ms"`
	OfferRateLimitPerSec          int `json:"offer_rate_limit_per_sec"`
	OfferBurst                    int `json:"offer_burst"`

	QDriftPctPerSec       float64 `json:"q_drift_pct_per_sec"`
	QUpdateIntervalMS     int     `json:"q_update_interval_ms"`
	QFreezeAfterMS        int     `json:"q_freeze_after_ms"`
	MinReparentIntervalMS int     `json:"min_reparent_interval_ms"`

	LeaderRotation           bool    `json:"leader_rotation"`
	RotationDropPct          float64 `json:"rotation_drop_pct"`
	RotationMinDelta         float64 `json:"rotation_min_delta"`
	RotationWindowMS         int     `json:"rotation_window_ms"`
	RotationMinTenureMS      int     `json:"rotation_min_tenure_ms"`
	RotationCooldownMS       int     `json:"rotation_cooldown_ms"`
	RotationCheckMS          int     `json:"rotation_check_ms"`
	RotationPrepareQuorumPct float64 `json:"rotation_prepare_quorum_pct"`
	RotationPrepareTimeoutMS int     `json:"rotation_prepare_timeout_ms"`
	RotationCommitQuorumPct  float64 `json:"rotation_commit_quorum_pct"`
	RotationCommitTimeoutMS  int     `json:"rotation_commit_timeout_ms"`
}

// DefaultStartConfig returns defaults close to realistic behavior while staying
// responsive for interactive visualization.
func DefaultStartConfig() StartConfig {
	return StartConfig{
		N:                 5,
		Y:                 3,
		Nodes:             120,
		TickMS:            50,
		InboxSize:         256,
		SubnetSize:        256,
		NodeGoroutines:    true,
		AsyncSpawn:        true,
		SpawnParallelism:  0,
		SpawnDelayMS:      0,
		ShutdownTimeoutMS: 5000,
		Seed:              0,

		UDPDemo:                       true,
		UDPIntervalMS:                 1000,
		UDPSender:                     "random",
		UDPKind:                       "DISCOVER",
		UDPPayload:                    "discover",
		UDPSubnets:                    1,
		UDPDelayMinMS:                 5,
		UDPDelayMaxMS:                 30,
		UDPDropRate:                   0.01,
		RandomDiscoverFanout:          4,
		RandomDiscoverBackoffBaseMS:   500,
		RandomDiscoverBackoffMaxMS:    8000,
		RandomDiscoverBackoffJitterMS: 250,
		OfferRateLimitPerSec:          25,
		OfferBurst:                    50,
		QDriftPctPerSec:               0.01,
		QUpdateIntervalMS:             1000,
		QFreezeAfterMS:                0,
		MinReparentIntervalMS:         0,

		LeaderRotation:           false,
		RotationDropPct:          0.20,
		RotationMinDelta:         0.05,
		RotationWindowMS:         5000,
		RotationMinTenureMS:      15000,
		RotationCooldownMS:       15000,
		RotationCheckMS:          1000,
		RotationPrepareQuorumPct: 0.67,
		RotationPrepareTimeoutMS: 1200,
		RotationCommitQuorumPct:  0.67,
		RotationCommitTimeoutMS:  1200,
	}
}
