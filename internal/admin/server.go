package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/proxy"
)

// StatsProvider supplies runtime statistics from the proxy server.
type StatsProvider interface {
	GetStats() proxy.ServerStats
}

// StatusResponse is returned by GET /api/status.
type StatusResponse struct {
	Mode            string                  `json:"mode"`
	PinnedAccountID string                  `json:"pinned_account_id"`
	Accounts        []*account.CloudAccount `json:"accounts"`
	Stats           proxy.ServerStats       `json:"stats"`
	Uptime          string                  `json:"uptime"`
}

// SelectRequest is the payload for POST /api/select.
type SelectRequest struct {
	AccountID string `json:"account_id"`
	Unpin     bool   `json:"unpin"`
}

// RefreshRequest is the payload for POST /api/refresh.
type RefreshRequest struct {
	AccountID string `json:"account_id"`
}

// ResponseMessage is a standard JSON response for commands.
type ResponseMessage struct {
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// AdminServer provides an internal HTTP API for administration and TUI monitoring.
type AdminServer struct {
	pool          *account.Pool
	statsProvider StatsProvider
	mux           *http.ServeMux
	startTime     time.Time

	mu         sync.RWMutex
	httpServer *http.Server
}

// NewAdminServer creates a new AdminServer instance and registers the administration endpoints.
func NewAdminServer(pool *account.Pool, statsProvider StatsProvider) *AdminServer {
	s := &AdminServer{
		pool:          pool,
		statsProvider: statsProvider,
		mux:           http.NewServeMux(),
		startTime:     time.Now(),
	}

	// Status endpoints
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/status", s.handleStatus)

	// Account selection endpoints
	s.mux.HandleFunc("/api/select", s.handleSelect)
	s.mux.HandleFunc("/select", s.handleSelect)

	// Configuration reload endpoints
	s.mux.HandleFunc("/api/reload", s.handleReload)
	s.mux.HandleFunc("/reload", s.handleReload)

	// Token refresh endpoints
	s.mux.HandleFunc("/api/refresh", s.handleRefresh)
	s.mux.HandleFunc("/refresh", s.handleRefresh)

	return s
}

// Handler returns the underlying http.Handler.
func (s *AdminServer) Handler() http.Handler {
	return s.mux
}

// ServeHTTP implements http.Handler.
func (s *AdminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Start starts listening on the specified address (defaults to 127.0.0.1:8046).
func (s *AdminServer) Start(addr string) error {
	listenAddr := addr
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8046"
	}

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	s.httpServer = srv
	s.mu.Unlock()

	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the admin HTTP server.
func (s *AdminServer) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	srv := s.httpServer
	s.mu.RUnlock()

	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}

func (s *AdminServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, ResponseMessage{Error: "method not allowed"})
		return
	}

	mode := "round-robin"
	pinnedAccountID := ""
	var accounts []*account.CloudAccount

	if s.pool != nil {
		isPinned, pid := s.pool.IsPinned()
		if isPinned {
			mode = "pinned"
			pinnedAccountID = pid
		}
		accounts = s.pool.GetAccounts()
	}

	var stats proxy.ServerStats
	if s.statsProvider != nil {
		stats = s.statsProvider.GetStats()
	}

	resp := StatusResponse{
		Mode:            mode,
		PinnedAccountID: pinnedAccountID,
		Accounts:        accounts,
		Stats:           stats,
		Uptime:          time.Since(s.startTime).Truncate(time.Second).String(),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *AdminServer) handleSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ResponseMessage{Error: "method not allowed"})
		return
	}

	var req SelectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ResponseMessage{Error: "invalid request body"})
		return
	}

	if !req.Unpin && strings.TrimSpace(req.AccountID) == "" {
		writeJSON(w, http.StatusBadRequest, ResponseMessage{Error: "either account_id or unpin must be provided"})
		return
	}

	if s.pool != nil {
		if req.Unpin {
			if err := s.pool.Unpin(); err != nil {
				writeJSON(w, http.StatusInternalServerError, ResponseMessage{Error: err.Error()})
				return
			}
		} else if req.AccountID != "" {
			if err := s.pool.PinAccount(req.AccountID); err != nil {
				writeJSON(w, http.StatusInternalServerError, ResponseMessage{Error: err.Error()})
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, ResponseMessage{Status: "ok"})
}

func (s *AdminServer) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ResponseMessage{Error: "method not allowed"})
		return
	}

	if s.pool != nil {
		if err := s.pool.Reload(); err != nil {
			writeJSON(w, http.StatusInternalServerError, ResponseMessage{Error: err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, ResponseMessage{Status: "ok"})
}

func (s *AdminServer) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ResponseMessage{Error: "method not allowed"})
		return
	}

	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ResponseMessage{Error: "invalid request body"})
		return
	}

	if req.AccountID == "" {
		writeJSON(w, http.StatusBadRequest, ResponseMessage{Error: "account_id is required"})
		return
	}

	if s.pool != nil {
		if err := s.pool.RefreshToken(req.AccountID); err != nil {
			writeJSON(w, http.StatusInternalServerError, ResponseMessage{Error: err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, ResponseMessage{Status: "ok"})
}

func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}
