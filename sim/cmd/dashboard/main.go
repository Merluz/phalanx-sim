package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"phalanxsim2/internal/dashboard"
)

func main() {
	listen := flag.String("listen", ":8090", "HTTP listen address")
	webDir := flag.String("web-dir", "./web", "path to web assets directory")
	stopOnExit := flag.Bool("stop-on-exit", true, "stop active simulation on process exit")
	logMode := flag.String("log-mode", "full", "log mode: full|progress|quiet")
	quietLogs := flag.Bool("quiet-logs", false, "disable runtime logs to improve performance")
	flag.Parse()

	mgr := dashboard.NewManager()
	srv, err := dashboard.NewServer(mgr, *webDir)
	if err != nil {
		log.Fatalf("server init failed: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		if *stopOnExit {
			_ = mgr.Stop()
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	configureLogMode(*logMode, *quietLogs)
	fmt.Printf("sim2 dashboard listening on http://localhost%s\n", *listen)
	fmt.Printf("open web UI: http://localhost%s/\n", *listen)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "http server failed: %v\n", err)
		os.Exit(1)
	}
}

func configureLogMode(mode string, quietOverride bool) {
	if quietOverride {
		mode = "quiet"
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "quiet":
		log.SetOutput(io.Discard)
	case "progress":
		log.SetOutput(&progressLogWriter{dst: os.Stderr})
	default:
		// full
	}
}

type progressLogWriter struct {
	dst io.Writer
}

func (w *progressLogWriter) Write(p []byte) (int, error) {
	line := string(p)
	keep := []string{
		"event=start_ok",
		"event=stop_ok",
		"event=spawn_progress",
		"event=spawn_done",
		"event=leader_rotate",
		"event=rotate_abort",
		"event=node_disconnected_detected",
	}
	for _, k := range keep {
		if strings.Contains(line, k) {
			_, _ = w.dst.Write(p)
			return len(p), nil
		}
	}
	low := strings.ToLower(line)
	if strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "panic") {
		_, _ = w.dst.Write(p)
	}
	return len(p), nil
}
