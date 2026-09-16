package tui

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/admin"
	"gemini-bridge/internal/proxy"

	tea "github.com/charmbracelet/bubbletea"
)

type mockAdminAPI struct {
	mu           sync.Mutex
	status       *admin.StatusResponse
	getStatusErr error

	pinnedID    string
	unpinned    bool
	refreshedID string
	reloaded    bool

	pinErr     error
	unpinErr   error
	refreshErr error
	reloadErr  error
}

func (m *mockAdminAPI) GetStatus() (*admin.StatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getStatusErr != nil {
		return nil, m.getStatusErr
	}
	return m.status, nil
}

func (m *mockAdminAPI) PinAccount(accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pinErr != nil {
		return m.pinErr
	}
	m.pinnedID = accountID
	m.unpinned = false
	if m.status != nil {
		m.status.Mode = "pinned"
		m.status.PinnedAccountID = accountID
	}
	return nil
}

func (m *mockAdminAPI) Unpin() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unpinErr != nil {
		return m.unpinErr
	}
	m.unpinned = true
	m.pinnedID = ""
	if m.status != nil {
		m.status.Mode = "round-robin"
		m.status.PinnedAccountID = ""
	}
	return nil
}

func (m *mockAdminAPI) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reloadErr != nil {
		return m.reloadErr
	}
	m.reloaded = true
	return nil
}

func (m *mockAdminAPI) RefreshToken(accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refreshErr != nil {
		return m.refreshErr
	}
	m.refreshedID = accountID
	return nil
}

func sampleStatus() *admin.StatusResponse {
	return &admin.StatusResponse{
		Mode:            "round-robin",
		PinnedAccountID: "",
		Uptime:          "1h23m45s",
		Stats: proxy.ServerStats{
			TotalRequests:     150,
			SuccessRequests:   140,
			RateLimitRequests: 7,
			ErrorRequests:     3,
			RecentLogs: []proxy.RequestLogEntry{
				{
					Timestamp:    time.Date(2026, 9, 9, 10, 15, 0, 0, time.UTC),
					Method:       "POST",
					Path:         "/v1/chat/completions",
					Model:        "claude-3-5-sonnet",
					Status:       200,
					Duration:     245 * time.Millisecond,
					AccountEmail: "active@example.com",
				},
				{
					Timestamp:    time.Date(2026, 9, 9, 10, 15, 30, 0, time.UTC),
					Method:       "POST",
					Path:         "/v1/messages",
					Model:        "gemini-2.5-pro",
					Status:       429,
					Duration:     110 * time.Millisecond,
					AccountEmail: "cooldown@example.com",
					Error:        "RESOURCE_EXHAUSTED",
				},
			},
		},
		Accounts: []*account.CloudAccount{
			{
				ID:       "acc-1",
				Email:    "active@example.com",
				Status:   "active",
				Provider: "google",
				Token: account.CloudToken{
					ProjectID:       "my-proj-1",
					ExpiryTimestamp: time.Now().Add(45 * time.Minute).Unix(),
				},
			},
			{
				ID:            "acc-2",
				Email:         "cooldown@example.com",
				Status:        "active",
				CooldownUntil: time.Now().Add(10 * time.Minute),
				Provider:      "google",
				Token: account.CloudToken{
					ProjectID:       "my-proj-2",
					ExpiryTimestamp: time.Now().Add(30 * time.Minute).Unix(),
				},
			},
			{
				ID:       "acc-3",
				Email:    "expired@example.com",
				Status:   "error",
				Provider: "google",
				Token: account.CloudToken{
					ProjectID:       "my-proj-3",
					ExpiryTimestamp: time.Now().Add(-10 * time.Minute).Unix(),
				},
			},
		},
	}
}

func TestModel_Init(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected Init to return a non-nil tea.Cmd")
	}
}

func TestModel_Update_StatusLoaded(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)

	// Send statusMsg
	updatedModel, _ := m.Update(statusMsg{status: mock.status})
	mod := updatedModel.(Model)

	if mod.status == nil {
		t.Fatal("expected status to be populated")
	}
	if len(mod.status.Accounts) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(mod.status.Accounts))
	}
	if mod.err != nil {
		t.Fatalf("expected nil err, got %v", mod.err)
	}

	// Test statusMsg with error
	errTest := errors.New("network failure")
	updatedModel2, _ := mod.Update(statusMsg{err: errTest})
	mod2 := updatedModel2.(Model)
	if mod2.err == nil || !strings.Contains(mod2.err.Error(), "network failure") {
		t.Fatalf("expected error to be recorded, got %v", mod2.err)
	}
}

func TestModel_Navigation(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)
	mModel, _ := m.Update(statusMsg{status: mock.status})
	m = mModel.(Model)

	if m.cursor != 0 {
		t.Fatalf("expected cursor 0, got %d", m.cursor)
	}

	// Navigate down with 'down' key
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = u.(Model)
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1, got %d", m.cursor)
	}

	// Navigate down with 'j' key
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = u.(Model)
	if m.cursor != 2 {
		t.Fatalf("expected cursor 2, got %d", m.cursor)
	}

	// Try navigating down past end
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = u.(Model)
	if m.cursor != 2 {
		t.Fatalf("expected cursor to stay at 2, got %d", m.cursor)
	}

	// Navigate up with 'up' key
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = u.(Model)
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1, got %d", m.cursor)
	}

	// Navigate up with 'k' key
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = u.(Model)
	if m.cursor != 0 {
		t.Fatalf("expected cursor 0, got %d", m.cursor)
	}

	// Try navigating up past beginning
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = u.(Model)
	if m.cursor != 0 {
		t.Fatalf("expected cursor to stay at 0, got %d", m.cursor)
	}
}

func TestModel_Key_PinAndUnpin(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)
	u, _ := m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	// 1. Press 'p' to pin acc-1
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = u.(Model)
	if cmd == nil {
		t.Fatal("expected command from pressing 'p'")
	}
	msg := cmd()
	res, ok := msg.(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("expected actionResultMsg with no err, got %#v", msg)
	}
	if mock.pinnedID != "acc-1" {
		t.Fatalf("expected mock pinnedID acc-1, got %s", mock.pinnedID)
	}

	// Update status so model knows it is pinned to acc-1
	u, _ = m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	// 2. Press 'Enter' while selected acc-1 -> should toggle and UNPIN
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if cmd == nil {
		t.Fatal("expected command from pressing Enter")
	}
	msg = cmd()
	res, ok = msg.(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("expected unpin actionResultMsg with no err, got %#v", msg)
	}
	if !mock.unpinned {
		t.Fatal("expected mock to be unpinned")
	}

	// Update status
	u, _ = m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	// 3. Move to acc-2 and press 'p' -> pin acc-2
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = u.(Model)
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = u.(Model)
	_ = cmd()
	if mock.pinnedID != "acc-2" {
		t.Fatalf("expected mock pinnedID acc-2, got %s", mock.pinnedID)
	}

	// 4. Press 'u' key -> unpin to round-robin
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	m = u.(Model)
	if cmd == nil {
		t.Fatal("expected command from pressing 'u'")
	}
	msg = cmd()
	res, ok = msg.(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("expected unpin actionResultMsg with no err, got %#v", msg)
	}
	if !mock.unpinned {
		t.Fatal("expected mock to be unpinned after 'u'")
	}
}

func TestModel_Key_RefreshToken(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)
	u, _ := m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	// Press 'r' on acc-1
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = u.(Model)
	if cmd == nil {
		t.Fatal("expected command from pressing 'r'")
	}
	msg := cmd()
	res, ok := msg.(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("expected refresh actionResultMsg, got %#v", msg)
	}
	if mock.refreshedID != "acc-1" {
		t.Fatalf("expected mock refreshedID acc-1, got %s", mock.refreshedID)
	}
}

func TestModel_Key_Reload(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)
	u, _ := m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	// Press 'R'
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = u.(Model)
	if cmd == nil {
		t.Fatal("expected command from pressing 'R'")
	}
	msg := cmd()
	res, ok := msg.(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("expected reload actionResultMsg, got %#v", msg)
	}
	if !mock.reloaded {
		t.Fatal("expected mock reloaded to be true")
	}
}

func TestModel_Key_Quit(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)

	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEsc},
	}

	for _, k := range keys {
		_, cmd := m.Update(k)
		if cmd == nil {
			t.Fatalf("expected quit command for key %v", k)
		}
	}
}

func TestModel_View_Rendering(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)

	// Test View before status is loaded
	initialView := m.View()
	if !strings.Contains(initialView, "Gemini Bridge") && !strings.Contains(initialView, "Loading") {
		t.Fatalf("expected initial view to contain title or loading state, got: %s", initialView)
	}

	// Update with status
	u, _ := m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	view := m.View()

	// 1. Header checks
	if !strings.Contains(view, "Gemini Bridge") {
		t.Error("view missing service title 'Gemini Bridge'")
	}
	if !strings.Contains(view, "1h23m45s") {
		t.Error("view missing uptime")
	}
	if !strings.Contains(view, "Round-Robin") {
		t.Error("view missing round-robin mode badge")
	}
	if !strings.Contains(view, "150") || !strings.Contains(view, "140") {
		t.Error("view missing request statistics (150 Total, 140 OK)")
	}

	// 2. Accounts table checks
	if !strings.Contains(view, "active@example.com") {
		t.Error("view missing active account email")
	}
	if !strings.Contains(view, "cooldown@example.com") {
		t.Error("view missing cooldown account email")
	}
	if !strings.Contains(view, "expired@example.com") {
		t.Error("view missing expired account email")
	}
	if !strings.Contains(view, "my-proj-1") {
		t.Error("view missing project ID")
	}
	if !strings.Contains(view, "ACTIVE") {
		t.Error("view missing ACTIVE status badge")
	}
	if !strings.Contains(view, "COOLDOWN") {
		t.Error("view missing COOLDOWN status badge")
	}
	if !strings.Contains(view, "EXPIRED") && !strings.Contains(view, "ERROR") {
		t.Error("view missing EXPIRED/ERROR status badge")
	}

	// 3. Activity panel checks
	if !strings.Contains(view, "claude-3-5-sonnet") {
		t.Error("view missing activity panel log model claude-3-5-sonnet")
	}
	if !strings.Contains(view, "/v1/chat/completions") {
		t.Error("view missing activity panel request path")
	}

	// 4. Help footer checks
	if !strings.Contains(view, "Navigate") || !strings.Contains(view, "Pin/Unpin") {
		t.Error("view missing keybindings in help footer")
	}

	// Test View in Pinned mode
	mock.status.Mode = "pinned"
	mock.status.PinnedAccountID = "acc-1"
	u, _ = m.Update(statusMsg{status: mock.status})
	m = u.(Model)
	pinnedView := m.View()
	if !strings.Contains(pinnedView, "Pinned") || !strings.Contains(pinnedView, "active@example.com") {
		t.Error("view in pinned mode should display Pinned and pinned account email")
	}
}

func TestModel_ActionStatusFeedback(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)
	u, _ := m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	// Successful action feedback
	u, _ = m.Update(actionResultMsg{action: "Pin", message: "Pinned to active@example.com"})
	m = u.(Model)
	view := m.View()
	if !strings.Contains(view, "Pinned to active@example.com") {
		t.Errorf("expected view to contain action feedback, got:\n%s", view)
	}

	// Error action feedback
	u, _ = m.Update(actionResultMsg{action: "Refresh", err: errors.New("refresh token expired")})
	m = u.(Model)
	view = m.View()
	if !strings.Contains(view, "refresh token expired") {
		t.Errorf("expected view to contain error feedback, got:\n%s", view)
	}
}

func TestModel_WindowSizeAndTick(t *testing.T) {
	mock := &mockAdminAPI{status: sampleStatus()}
	m := NewModel(mock)

	// Window resize
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = u.(Model)
	if m.width != 120 || m.height != 40 {
		t.Fatalf("expected 120x40, got %dx%d", m.width, m.height)
	}

	// Tick message
	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected batch command from tickMsg")
	}
}

func TestModel_ActionErrorsAndNilClient(t *testing.T) {
	mock := &mockAdminAPI{
		status:     sampleStatus(),
		pinErr:     errors.New("pin failed"),
		unpinErr:   errors.New("unpin failed"),
		refreshErr: errors.New("refresh failed"),
		reloadErr:  errors.New("reload failed"),
	}
	m := NewModel(mock)
	u, _ := m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	// Pin error
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	msg := cmd()
	res := msg.(actionResultMsg)
	if res.err == nil || !strings.Contains(res.err.Error(), "pin failed") {
		t.Fatalf("expected pin failed, got %#v", res)
	}

	// Unpin error
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	msg = cmd()
	res = msg.(actionResultMsg)
	if res.err == nil || !strings.Contains(res.err.Error(), "unpin failed") {
		t.Fatalf("expected unpin failed, got %#v", res)
	}

	// Refresh error
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	msg = cmd()
	res = msg.(actionResultMsg)
	if res.err == nil || !strings.Contains(res.err.Error(), "refresh failed") {
		t.Fatalf("expected refresh failed, got %#v", res)
	}

	// Reload error
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	msg = cmd()
	res = msg.(actionResultMsg)
	if res.err == nil || !strings.Contains(res.err.Error(), "reload failed") {
		t.Fatalf("expected reload failed, got %#v", res)
	}

	// Nil client handling
	nilModel := NewModel(nil)
	nilModel.status = sampleStatus()

	_, cmd = nilModel.handlePinToggle()
	res = cmd().(actionResultMsg)
	if res.err == nil {
		t.Fatal("expected error with nil client")
	}

	_, cmd = nilModel.handleUnpin()
	res = cmd().(actionResultMsg)
	if res.err == nil {
		t.Fatal("expected error with nil client")
	}

	_, cmd = nilModel.handleRefresh()
	res = cmd().(actionResultMsg)
	if res.err == nil {
		t.Fatal("expected error with nil client")
	}

	_, cmd = nilModel.handleReload()
	res = cmd().(actionResultMsg)
	if res.err == nil {
		t.Fatal("expected error with nil client")
	}

	sMsg := nilModel.fetchStatusCmd()()
	if sMsg.(statusMsg).err == nil {
		t.Fatal("expected error fetching status with nil client")
	}
}

func TestModel_EmptyAccountsAndQuittingView(t *testing.T) {
	mock := &mockAdminAPI{
		status: &admin.StatusResponse{
			Mode:     "round-robin",
			Accounts: []*account.CloudAccount{},
			Stats: proxy.ServerStats{
				RecentLogs: []proxy.RequestLogEntry{
					{
						Timestamp: time.Now(),
						Method:    "GET",
						Status:    500,
						Duration:  500 * time.Microsecond,
						Model:     "claude-3-opus",
						Path:      "/v1/models",
					},
					{
						Timestamp: time.Now(),
						Method:    "POST",
						Status:    200,
						Duration:  1500 * time.Millisecond,
						Model:     "gpt-4o",
						Path:      "/v1/chat/completions",
					},
				},
			},
		},
	}
	m := NewModel(mock)
	u, _ := m.Update(statusMsg{status: mock.status})
	m = u.(Model)

	// In empty accounts, pin/refresh should do nothing
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected nil cmd when no accounts")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil {
		t.Fatal("expected nil cmd when no accounts")
	}

	view := m.View()
	if !strings.Contains(view, "No accounts loaded") {
		t.Errorf("expected 'No accounts loaded' in view, got: %s", view)
	}

	// Quitting view
	m.quitting = true
	quitView := m.View()
	if !strings.Contains(quitView, "Detached") {
		t.Errorf("expected detached message in quit view, got: %s", quitView)
	}
}

func TestView_FormattingHelpers(t *testing.T) {
	// Duration formatting
	if formatDuration(500*time.Microsecond) != "500µs" {
		t.Errorf("unexpected microsecond format: %s", formatDuration(500*time.Microsecond))
	}
	if formatDuration(45*time.Millisecond) != "45ms" {
		t.Errorf("unexpected millisecond format: %s", formatDuration(45*time.Millisecond))
	}
	if formatDuration(3*time.Second+500*time.Millisecond) != "3.50s" {
		t.Errorf("unexpected second format: %s", formatDuration(3*time.Second+500*time.Millisecond))
	}

	// Truncate string
	if truncateString("short", 10) != "short" {
		t.Errorf("unexpected truncate result: %s", truncateString("short", 10))
	}
	if truncateString("toolongstring", 8) != "toolo..." {
		t.Errorf("unexpected truncate result: %s", truncateString("toolongstring", 8))
	}
	if truncateString("test", 2) != "te" {
		t.Errorf("unexpected truncate result: %s", truncateString("test", 2))
	}
	if truncateString("test", 0) != "" {
		t.Errorf("unexpected truncate result: %s", truncateString("test", 0))
	}
}

func TestRenderActivityPanel_ThirtyRows(t *testing.T) {
	logs := make([]proxy.RequestLogEntry, 60)
	for i := 0; i < 60; i++ {
		logs[i] = proxy.RequestLogEntry{
			Timestamp:    time.Date(2026, 9, 16, 8, 0, i%60, 0, time.UTC),
			Method:       "POST",
			Path:         fmt.Sprintf("/v1/chat/%d", i),
			Model:        "gemini-3-flash",
			Status:       200,
			Duration:     100 * time.Millisecond,
			AccountEmail: "test@example.com",
		}
	}

	stats := proxy.ServerStats{RecentLogs: logs}
	output := renderActivityPanel(stats, 100)

	// Should contain the last entry (/v1/chat/59)
	if !strings.Contains(output, "/v1/chat/59") {
		t.Errorf("expected output to contain last entry /v1/chat/59")
	}
	// Should contain /v1/chat/30 (the 30th from end: 60 - 30 = index 30)
	if !strings.Contains(output, "/v1/chat/30") {
		t.Errorf("expected output to contain entry /v1/chat/30")
	}
	// Should NOT contain /v1/chat/29 (index 29 is excluded since only last 30 are shown)
	if strings.Contains(output, "/v1/chat/29 ") {
		t.Errorf("expected output NOT to contain entry /v1/chat/29")
	}

	// Count lines containing /v1/chat
	chatLines := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "/v1/chat/") {
			chatLines++
		}
	}
	if chatLines != 30 {
		t.Errorf("expected exactly 30 log rows, got %d", chatLines)
	}
}


