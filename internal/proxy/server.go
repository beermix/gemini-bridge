package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/google"
)

// ServerConfig configures proxy listener address and access key.
type ServerConfig struct {
	Addr   string
	APIKey string
}

// RequestLogEntry holds diagnostic details about an incoming request.
type RequestLogEntry struct {
	Timestamp    time.Time     `json:"timestamp"`
	Method       string        `json:"method"`
	Path         string        `json:"path"`
	Model        string        `json:"model"`
	TargetModel  string        `json:"target_model"`
	Status       int           `json:"status"`
	Duration     time.Duration `json:"duration"`
	AccountEmail string        `json:"account_email"`
	Endpoint     string        `json:"endpoint,omitempty"`
	Error        string        `json:"error,omitempty"`
}

// ServerStats tracks real-time performance and request metrics.
type ServerStats struct {
	TotalRequests     int64             `json:"total_requests"`
	SuccessRequests   int64             `json:"success_requests"`
	RateLimitRequests int64             `json:"rate_limit_requests"`
	ErrorRequests     int64             `json:"error_requests"`
	ActiveEndpoint    string            `json:"active_endpoint"`
	RecentLogs        []RequestLogEntry `json:"recent_logs"`
	RecentErrors      []RequestLogEntry `json:"recent_errors"`
}

// Server provides HTTP proxying between OpenAI/Anthropic SDKs and Google Cloud Code upstream.
type Server struct {
	cfg        ServerConfig
	pool       *account.Pool
	client     *google.Client
	httpServer *http.Server
	mux        *http.ServeMux
	handler    http.Handler
	startTime  time.Time

	mu    sync.RWMutex
	stats ServerStats
}

// NewServer initializes Server and registers OpenAI, Anthropic, and Health endpoints.
func NewServer(cfg ServerConfig, pool *account.Pool, client *google.Client) *Server {
	s := &Server{
		cfg:       cfg,
		pool:      pool,
		client:    client,
		startTime: time.Now(),
		mux:       http.NewServeMux(),
		stats: ServerStats{
			RecentLogs:   make([]RequestLogEntry, 0, 50),
			RecentErrors: make([]RequestLogEntry, 0, 20),
		},
	}

	// OpenAI endpoints
	s.mux.HandleFunc("/v1/chat/completions", s.handleOpenAIChatCompletions)
	s.mux.HandleFunc("/chat/completions", s.handleOpenAIChatCompletions)
	s.mux.HandleFunc("/v1/models", s.handleOpenAIModels)
	s.mux.HandleFunc("/v1/models/", s.handleOpenAIModels)
	s.mux.HandleFunc("/models", s.handleOpenAIModels)
	s.mux.HandleFunc("/models/", s.handleOpenAIModels)

	// Anthropic endpoints
	s.mux.HandleFunc("/v1/messages", s.handleAnthropicMessages)
	s.mux.HandleFunc("/messages", s.handleAnthropicMessages)
	s.mux.HandleFunc("/v1/messages/count_tokens", s.handleAnthropicCountTokens)

	// Health endpoint
	s.mux.HandleFunc("/health", s.handleHealth)

	s.handler = s.authMiddleware(s.mux)
	return s
}

// Handler returns the HTTP handler with auth middleware applied.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Pool returns the underlying Account Pool.
func (s *Server) Pool() *account.Pool {
	return s.pool
}

// Start runs the HTTP proxy listener on the provided address (or configured default).
func (s *Server) Start(addr string) error {
	listenAddr := addr
	if listenAddr == "" {
		listenAddr = s.cfg.Addr
	}
	if listenAddr == "" {
		listenAddr = ":8045"
	}

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: s.handler,
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

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	srv := s.httpServer
	s.mu.RUnlock()

	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}

// GetStats returns a snapshot of proxy operational metrics and recent logs.
func (s *Server) GetStats() ServerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	logsCopy := make([]RequestLogEntry, len(s.stats.RecentLogs))
	copy(logsCopy, s.stats.RecentLogs)

	errorsCopy := make([]RequestLogEntry, len(s.stats.RecentErrors))
	copy(errorsCopy, s.stats.RecentErrors)

	activeEP := "cloudcode-pa"
	if s.client != nil {
		activeEP = s.client.ActiveEndpoint()
	}

	return ServerStats{
		TotalRequests:     s.stats.TotalRequests,
		SuccessRequests:   s.stats.SuccessRequests,
		RateLimitRequests: s.stats.RateLimitRequests,
		ErrorRequests:     s.stats.ErrorRequests,
		ActiveEndpoint:    activeEP,
		RecentLogs:        logsCopy,
		RecentErrors:      errorsCopy,
	}
}

func (s *Server) recordRateLimit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.RateLimitRequests++
}

func (s *Server) recordRequest(entry RequestLogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.Endpoint == "" && s.client != nil {
		entry.Endpoint = s.client.ActiveEndpoint()
	}

	s.stats.TotalRequests++
	isErr := (entry.Status >= 400 || entry.Error != "") && entry.Status != http.StatusTooManyRequests
	if entry.Status >= 200 && entry.Status < 400 && entry.Error == "" {
		s.stats.SuccessRequests++
	} else if entry.Status == http.StatusTooManyRequests {
		s.stats.RateLimitRequests++
	} else {
		s.stats.ErrorRequests++
	}

	const maxRecentLogs = 50
	if len(s.stats.RecentLogs) >= maxRecentLogs {
		s.stats.RecentLogs = s.stats.RecentLogs[1:]
	}
	s.stats.RecentLogs = append(s.stats.RecentLogs, entry)

	if isErr || entry.Status == http.StatusTooManyRequests {
		const maxRecentErrors = 20
		if len(s.stats.RecentErrors) >= maxRecentErrors {
			s.stats.RecentErrors = s.stats.RecentErrors[1:]
		}
		s.stats.RecentErrors = append(s.stats.RecentErrors, entry)
	}

	accStr := ""
	if entry.AccountEmail != "" {
		accStr = fmt.Sprintf(" [acc: %s]", entry.AccountEmail)
	}
	epStr := ""
	if entry.Endpoint != "" {
		epStr = fmt.Sprintf(" [ep: %s]", entry.Endpoint)
	}
	errStr := ""
	if entry.Error != "" {
		errStr = fmt.Sprintf(" - error: %s", entry.Error)
	}
	modelStr := ""
	if entry.Model != "" {
		if entry.TargetModel != "" && entry.TargetModel != entry.Model {
			modelStr = fmt.Sprintf(" [%s -> %s]", entry.Model, entry.TargetModel)
		} else {
			modelStr = fmt.Sprintf(" [%s]", entry.Model)
		}
	}
	log.Printf("[proxy] %s %s%s -> %d in %v%s%s%s", entry.Method, entry.Path, modelStr, entry.Status, entry.Duration.Round(time.Millisecond), accStr, epStr, errStr)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	accountsTotal := 0
	accountsActive := 0
	if s.pool != nil {
		accounts := s.pool.GetAccounts()
		accountsTotal = len(accounts)
		for _, acc := range accounts {
			if acc.Status != "disabled" && acc.Status != "inactive" && !acc.IsCooldown() {
				accountsActive++
			}
		}
	}

	resp := map[string]interface{}{
		"status":          "ok",
		"version":         "gemini-bridge/1.0",
		"uptime":          time.Since(s.startTime).String(),
		"accounts_total":  accountsTotal,
		"accounts_active": accountsActive,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func generateID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func readSSEData(reader *bufio.Reader, onData func(payload []byte) error) error {
	var dataBuf bytes.Buffer
	for {
		line, err := reader.ReadBytes('\n')
		trimmed := bytes.TrimRight(line, "\r\n")

		if len(trimmed) == 0 {
			if dataBuf.Len() > 0 {
				payload := make([]byte, dataBuf.Len())
				copy(payload, dataBuf.Bytes())
				dataBuf.Reset()
				if err := onData(payload); err != nil {
					return err
				}
			}
		} else if bytes.HasPrefix(trimmed, []byte("data:")) {
			chunk := bytes.TrimSpace(trimmed[5:])
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.Write(chunk)
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				if dataBuf.Len() > 0 {
					_ = onData(dataBuf.Bytes())
				}
				return nil
			}
			return err
		}
	}
}

// readFirstSSEPayload reads lines until the first non-empty SSE data payload is found,
// honoring context cancellation to avoid goroutine or request leaks.
func readFirstSSEPayload(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan sseChunkResult, 1)

	go func() {
		var dataBuf bytes.Buffer
		for {
			line, err := reader.ReadBytes('\n')
			trimmed := bytes.TrimRight(line, "\r\n")

			if len(trimmed) == 0 {
				if dataBuf.Len() > 0 {
					payload := make([]byte, dataBuf.Len())
					copy(payload, dataBuf.Bytes())
					ch <- sseChunkResult{payload: payload}
					return
				}
			} else if bytes.HasPrefix(trimmed, []byte("data:")) {
				chunk := bytes.TrimSpace(trimmed[5:])
				if dataBuf.Len() > 0 {
					dataBuf.WriteByte('\n')
				}
				dataBuf.Write(chunk)
			}

			if err != nil {
				if dataBuf.Len() > 0 {
					payload := make([]byte, dataBuf.Len())
					copy(payload, dataBuf.Bytes())
					ch <- sseChunkResult{payload: payload}
					return
				}
				ch <- sseChunkResult{err: err}
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.payload, res.err
	}
}

type sseChunkResult struct {
	payload []byte
	err     error
}

// readSSEDataWithKeepalive reads SSE events from reader, invoking onData for each payload,
// and calling onIdle periodically if no data is received within idleInterval.
func readSSEDataWithKeepalive(ctx context.Context, reader *bufio.Reader, idleInterval time.Duration, onIdle func(), onData func(payload []byte) error) error {
	ch := make(chan sseChunkResult, 16)

	go func() {
		defer close(ch)
		var dataBuf bytes.Buffer
		for {
			line, err := reader.ReadBytes('\n')
			trimmed := bytes.TrimRight(line, "\r\n")

			if len(trimmed) == 0 {
				if dataBuf.Len() > 0 {
					payload := make([]byte, dataBuf.Len())
					copy(payload, dataBuf.Bytes())
					dataBuf.Reset()
					select {
					case ch <- sseChunkResult{payload: payload}:
					case <-ctx.Done():
						return
					}
				}
			} else if bytes.HasPrefix(trimmed, []byte("data:")) {
				chunk := bytes.TrimSpace(trimmed[5:])
				if dataBuf.Len() > 0 {
					dataBuf.WriteByte('\n')
				}
				dataBuf.Write(chunk)
			}

			if err != nil {
				if dataBuf.Len() > 0 {
					payload := make([]byte, dataBuf.Len())
					copy(payload, dataBuf.Bytes())
					select {
					case ch <- sseChunkResult{payload: payload}:
					case <-ctx.Done():
						return
					}
				}
				if !errors.Is(err, io.EOF) {
					select {
					case ch <- sseChunkResult{err: err}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()

	ticker := time.NewTicker(idleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case res, ok := <-ch:
			if !ok {
				return nil
			}
			if res.err != nil {
				return res.err
			}
			ticker.Reset(idleInterval)
			if err := onData(res.payload); err != nil {
				return err
			}
		case <-ticker.C:
			if onIdle != nil {
				onIdle()
			}
		}
	}
}

// formatAnthropicError returns JSON-formatted error matching Anthropic API specification.
func formatAnthropicError(status int, message string) []byte {
	errType := "api_error"
	if status == http.StatusBadRequest {
		errType = "invalid_request_error"
	} else if status == http.StatusUnauthorized {
		errType = "authentication_error"
	} else if status == http.StatusTooManyRequests {
		errType = "rate_limit_error"
	}

	m := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errType,
			"message": message,
		},
	}
	b, _ := json.Marshal(m)
	return b
}

// formatOpenAIError returns JSON-formatted error matching OpenAI API specification.
func formatOpenAIError(status int, message string) []byte {
	errType := "server_error"
	code := fmt.Sprintf("%d", status)
	if status == http.StatusBadRequest {
		errType = "invalid_request_error"
		code = "bad_request"
	} else if status == http.StatusUnauthorized {
		errType = "invalid_request_error"
		code = "invalid_api_key"
	} else if status == http.StatusTooManyRequests {
		errType = "rate_limit_error"
		code = "rate_limit_exceeded"
	}

	m := map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	}
	b, _ := json.Marshal(m)
	return b
}
