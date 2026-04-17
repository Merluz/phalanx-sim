package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"phalanxsim2/internal/mesh"
	"phalanxsim2/internal/node"
	"phalanxsim2/internal/node/connsim"
)

func main() {
	cfg := mesh.DefaultConfig()

	nodesDefault := 1000
	seedDefault := time.Now().UnixNano()

	var (
		nodes            = flag.Int("n", nodesDefault, "number of nodes to spawn at startup")
		tick             = flag.Duration("tick", cfg.TickPeriod, "node tick period (example: 20ms, 1s)")
		inboxSize        = flag.Int("inbox-size", cfg.InboxSize, "inbox channel size per node")
		subnetSize       = flag.Uint64("subnet-size", cfg.SubnetSize, "simulated subnet bucket size")
		nodeGoroutines   = flag.Bool("node-goroutines", cfg.NodeGoroutines, "if true, each node runs in its own goroutine")
		asyncSpawn       = flag.Bool("async-spawn", cfg.AsyncSpawn, "if true, startup spawn is parallelized")
		spawnParallelism = flag.Int("spawn-parallelism", cfg.SpawnParallelism, "max parallel spawn workers; 0 uses GOMAXPROCS")
		runFor           = flag.Duration("run-for", 0, "optional run duration; 0 means run until Ctrl+C")
		statsEvery       = flag.Duration("stats-every", 2*time.Second, "how often to print runtime stats")
		shutdownTimeout  = flag.Duration("shutdown-timeout", 5*time.Second, "max wait time for graceful shutdown")
		seed             = flag.Int64("seed", seedDefault, "RNG seed for reproducibility")
		udpDemo          = flag.Bool("udp-demo", false, "enable UDP broadcast simulation demo")
		udpInterval      = flag.Duration("udp-interval", 2*time.Second, "interval for periodic UDP broadcast demo")
		udpSender        = flag.String("udp-sender", "root", "UDP sender selection: root|random")
		udpKind          = flag.String("udp-kind", "DISCOVER", "UDP datagram kind label")
		udpPayload       = flag.String("udp-payload", "hello-from-udp-demo", "UDP datagram payload text")
		udpSubnets       = flag.Int("udp-subnets", 1, "number of simulated UDP subnets for demo registration")
		udpDelayMin      = flag.Duration("udp-delay-min", 2*time.Millisecond, "minimum UDP delivery delay")
		udpDelayMax      = flag.Duration("udp-delay-max", 12*time.Millisecond, "maximum UDP delivery delay")
		udpDropRate      = flag.Float64("udp-drop-rate", 0, "UDP packet drop rate in [0,1]")
	)

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "PHALANX sim2 scaffold\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  go run ./cmd/sim [flags] [N]\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\nNotes:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  - Positional N is supported for backward compatibility.\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  - This scaffold intentionally has no node behavior logic yet.\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  - Step 1 transport demo: enable with -udp-demo for simulated UDP broadcast.\n")
	}

	flag.Parse()

	if flag.NArg() > 0 {
		if v, err := strconv.Atoi(flag.Arg(0)); err == nil && v >= 0 {
			*nodes = v
		} else {
			log.Fatalf("invalid positional node count %q", flag.Arg(0))
		}
	}

	cfg.TickPeriod = *tick
	cfg.InboxSize = *inboxSize
	cfg.SubnetSize = *subnetSize
	cfg.NodeGoroutines = *nodeGoroutines
	cfg.AsyncSpawn = *asyncSpawn
	cfg.SpawnParallelism = *spawnParallelism

	if cfg.SpawnParallelism <= 0 {
		cfg.SpawnParallelism = runtime.GOMAXPROCS(0)
	}
	if *nodes < 0 {
		log.Fatalf("n must be >= 0")
	}

	rng := rand.New(rand.NewSource(*seed))
	m := mesh.New(cfg, rng)

	log.Printf(
		"sim2 scaffold starting: nodes=%d tick=%s inbox=%d subnet=%d node_goroutines=%t async_spawn=%t spawn_parallelism=%d seed=%d",
		*nodes, cfg.TickPeriod, cfg.InboxSize, cfg.SubnetSize, cfg.NodeGoroutines, cfg.AsyncSpawn, cfg.SpawnParallelism, *seed,
	)

	root := m.Bootstrap()
	log.Printf("bootstrapped root leader: id=%d", root.ID)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	start := time.Now()
	if err := m.SpawnNodes(ctx, *nodes, cfg.AsyncSpawn); err != nil {
		log.Fatalf("spawn failed: %v", err)
	}
	log.Printf("spawn completed: nodes=%d in %s", *nodes, time.Since(start).Round(time.Millisecond))

	if *udpDemo {
		if *udpSubnets <= 0 {
			log.Fatalf("udp-subnets must be >= 1")
		}
		if *udpDropRate < 0 || *udpDropRate > 1 {
			log.Fatalf("udp-drop-rate must be in [0,1]")
		}
		if *udpInterval <= 0 {
			log.Fatalf("udp-interval must be > 0")
		}
		if *udpDelayMax < *udpDelayMin {
			log.Fatalf("udp-delay-max must be >= udp-delay-min")
		}
		if *udpSender != "root" && *udpSender != "random" {
			log.Fatalf("udp-sender must be root or random")
		}

		fcfg := connsim.DefaultConfig()
		fcfg.MinDelay = *udpDelayMin
		fcfg.MaxDelay = *udpDelayMax
		fcfg.DropRate = *udpDropRate
		fabric := connsim.NewFabric(fcfg, rand.New(rand.NewSource(*seed+1001)))

		for _, n := range m.NodesSnapshot() {
			subnet := connsim.NodeSubnetForIndex(n.ID, *udpSubnets)
			fabric.RegisterNode(n, subnet)
		}
		log.Printf(
			"udp demo ready: sender_mode=%s kind=%s subnets=%d delay_min=%s delay_max=%s drop_rate=%.3f interval=%s",
			*udpSender, *udpKind, *udpSubnets, *udpDelayMin, *udpDelayMax, *udpDropRate, *udpInterval,
		)

		var traceSeq atomic.Uint64
		go func() {
			ticker := time.NewTicker(*udpInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					var sender node.NodeID
					switch *udpSender {
					case "root":
						sender = m.RootID()
					case "random":
						if r := m.RandomNode(0); r != nil {
							sender = r.ID
						}
					}
					if sender == 0 {
						log.Printf("comp=main event=udp_broadcast_skip reason=no_sender")
						continue
					}
					trace := connsim.NewTraceID("udp", traceSeq.Add(1))
					sent := fabric.Broadcast(sender, *udpKind, []byte(*udpPayload), trace)
					log.Printf(
						"comp=main event=udp_broadcast_trigger sender=%d kind=%s trace=%s scheduled=%d",
						sender, *udpKind, trace, sent,
					)
				}
			}
		}()
	}

	if *runFor > 0 {
		go func() {
			timer := time.NewTimer(*runFor)
			defer timer.Stop()
			select {
			case <-timer.C:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	// Runtime stats loop.
	go func() {
		if *statsEvery <= 0 {
			return
		}
		ticker := time.NewTicker(*statsEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s := m.Stats()
				log.Printf(
					"stats: total=%d leaders=%d senders=%d base=%d running=%d subnets=%d",
					s.TotalNodes, s.Leaders, s.Senders, s.Base, s.RunningNodes, s.Subnets,
				)
			}
		}
	}()

	log.Printf("simulation running (scaffold mode). Ctrl+C to stop")
	<-ctx.Done()

	if err := m.Shutdown(*shutdownTimeout); err != nil {
		log.Printf("shutdown warning: %v", err)
	} else {
		log.Printf("shutdown complete")
	}
}
