package dashboard

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Server exposes dashboard APIs + static web assets.
type Server struct {
	mgr    *Manager
	webDir string
	mux    *http.ServeMux
}

// NewServer creates a dashboard server.
func NewServer(mgr *Manager, webDir string) (*Server, error) {
	if mgr == nil {
		return nil, errors.New("manager is nil")
	}
	if webDir == "" {
		return nil, errors.New("webDir is empty")
	}
	if st, err := os.Stat(webDir); err != nil || !st.IsDir() {
		return nil, errors.New("webDir does not exist or is not a directory")
	}

	s := &Server{
		mgr:    mgr,
		webDir: webDir,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

// Handler returns server HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/api/defaults", s.handleDefaults)
	s.mux.HandleFunc("/api/state", s.handleState)
	s.mux.HandleFunc("/api/metrics", s.handleMetrics)
	s.mux.HandleFunc("/api/metrics/recovery/current", s.handleRecoveryCurrent)
	s.mux.HandleFunc("/api/metrics/recovery/epochs", s.handleRecoveryEpochs)
	s.mux.HandleFunc("/api/metrics/recovery/epochs/", s.handleRecoveryEpochByID)
	s.mux.HandleFunc("/api/nodes", s.handleNodes)
	s.mux.HandleFunc("/api/nodes/spawn", s.handleSpawnNodes)
	s.mux.HandleFunc("/api/nodes/kill", s.handleKillNodes)
	s.mux.HandleFunc("/api/nodes/restart", s.handleRestartNodes)
	s.mux.HandleFunc("/api/nodes/restart-random", s.handleRestartRandomNodes)
	s.mux.HandleFunc("/api/topology", s.handleTopology)
	s.mux.HandleFunc("/api/start", s.handleStart)
	s.mux.HandleFunc("/api/stop", s.handleStop)
	s.mux.HandleFunc("/api/reset-edges", s.handleResetEdges)

	// Static UI.
	s.mux.HandleFunc("/", s.handleIndex)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		http.ServeFile(w, r, filepath.Join(s.webDir, "index.html"))
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleDefaults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.Defaults())
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.State())
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.Metrics())
}

func (s *Server) handleRecoveryCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cur, ok := s.mgr.RecoveryCurrent()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"current": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": cur})
}

func (s *Server) handleRecoveryEpochs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = n
	}
	if limit <= 0 {
		limit = 20
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"epochs": s.mgr.RecoveryEpochs(limit),
	})
}

func (s *Server) handleRecoveryEpochByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/metrics/recovery/epochs/"))
	if id == "" {
		http.Error(w, "missing epoch_id", http.StatusBadRequest)
		return
	}
	ep, ok := s.mgr.RecoveryEpochByID(id)
	if !ok {
		http.Error(w, "epoch not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q, err := parseNodesQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.Nodes(q))
}

func (s *Server) handleSpawnNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	req := SpawnNodesRequest{Async: true}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	res, err := s.mgr.SpawnNodes(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("comp=dashboard event=spawn_nodes_ok requested=%d spawned=%d before=%d after=%d", res.Requested, res.Spawned, res.BeforeTotal, res.AfterTotal)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleKillNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	req := KillNodesRequest{ExcludeRoot: true, MinLeadersKeep: 1}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	res, err := s.mgr.KillNodes(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("comp=dashboard event=kill_nodes_ok mode=%s requested=%d removed=%d before=%d after=%d", req.Mode, res.Requested, res.RemovedCount, res.BeforeTotal, res.AfterTotal)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRestartNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	req := RestartNodesRequest{ExcludeRoot: true}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	res, err := s.mgr.RestartNodes(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("comp=dashboard event=restart_nodes_ok requested=%d restarted=%d before=%d after=%d", res.Requested, res.Restarted, res.BeforeTotal, res.AfterTotal)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRestartRandomNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	req := RestartRandomNodesRequest{Mode: "count", ExcludeRoot: true}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	res, err := s.mgr.RestartRandomNodes(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("comp=dashboard event=restart_random_nodes_ok mode=%s requested=%d restarted=%d before=%d after=%d", req.Mode, res.Requested, res.Restarted, res.BeforeTotal, res.AfterTotal)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	topo := s.mgr.Topology()
	buf, err := json.Marshal(topo)
	if err != nil {
		http.Error(w, "failed to encode topology", http.StatusInternalServerError)
		return
	}
	s.mgr.setLastTopologyPayloadBytes(len(buf))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf)
}

func parseNodesQuery(r *http.Request) (NodesQuery, error) {
	qp := r.URL.Query()
	out := NodesQuery{
		Limit:  100,
		Offset: 0,
		Role:   qp.Get("role"),
		State:  qp.Get("state"),
		SortBy: qp.Get("sort"),
		Order:  qp.Get("order"),
	}
	if v := strings.TrimSpace(qp.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return NodesQuery{}, errors.New("invalid limit")
		}
		out.Limit = n
	}
	if v := strings.TrimSpace(qp.Get("offset")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return NodesQuery{}, errors.New("invalid offset")
		}
		out.Offset = n
	}
	if v := strings.TrimSpace(qp.Get("level")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return NodesQuery{}, errors.New("invalid level")
		}
		out.Level = n
		out.HasLevel = true
	}

	role := strings.ToLower(strings.TrimSpace(out.Role))
	switch role {
	case "", "all", "leader", "sender", "base":
	default:
		return NodesQuery{}, errors.New("invalid role")
	}

	state := strings.ToLower(strings.TrimSpace(out.State))
	switch state {
	case "", "all", "solo", "joining", "connected", "transitioning", "disconnected":
	default:
		return NodesQuery{}, errors.New("invalid state")
	}

	sortBy := strings.ToLower(strings.TrimSpace(out.SortBy))
	switch sortBy {
	case "", "id", "q", "level", "active_links":
	default:
		return NodesQuery{}, errors.New("invalid sort")
	}

	order := strings.ToLower(strings.TrimSpace(out.Order))
	switch order {
	case "", "asc", "desc":
	default:
		return NodesQuery{}, errors.New("invalid order")
	}

	return out, nil
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	cfg := s.mgr.Defaults()
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if err := s.mgr.Start(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("comp=dashboard event=start_ok nodes=%d", cfg.Nodes)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.mgr.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("comp=dashboard event=stop_ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleResetEdges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.mgr.ResetEdges(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("comp=dashboard event=reset_edges_ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
