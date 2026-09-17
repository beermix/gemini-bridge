package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gemini-bridge/internal/admin"

	tea "github.com/charmbracelet/bubbletea"
)

// AdminAPI defines the interface required by the TUI to communicate with the admin server.
type AdminAPI interface {
	GetStatus() (*admin.StatusResponse, error)
	PinAccount(accountID string) error
	Unpin() error
	Reload() error
	RefreshToken(accountID string) error
}

type tickMsg time.Time

type statusMsg struct {
	status *admin.StatusResponse
	err    error
}

type actionResultMsg struct {
	action  string
	message string
	err     error
}

// Model represents the Bubbletea application state for the TUI.
type Model struct {
	client       AdminAPI
	status       *admin.StatusResponse
	err          error
	cursor       int
	actionMsg    string
	actionMsgErr bool
	width        int
	height       int
	quitting     bool
}

// NewModel creates an initialized TUI Model.
func NewModel(client AdminAPI) Model {
	return Model{
		client: client,
		width:  125,
		height: 35,
	}
}

// Init initializes the model, fetching the initial status and setting up a 1-second refresh tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchStatusCmd(),
		m.tickCmd(),
	)
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) fetchStatusCmd() tea.Cmd {
	return func() tea.Msg {
		if m.client == nil {
			return statusMsg{err: errors.New("admin client not initialized")}
		}
		status, err := m.client.GetStatus()
		return statusMsg{status: status, err: err}
	}
}

// Update handles incoming Bubbletea messages and key events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp:
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case tea.KeyDown:
			if m.status != nil && m.cursor < len(m.status.Accounts)-1 {
				m.cursor++
			}
			return m, nil
		case tea.KeyEnter:
			return m.handlePinToggle()
		}

		switch msg.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "j":
			if m.status != nil && m.cursor < len(m.status.Accounts)-1 {
				m.cursor++
			}
			return m, nil
		case "p":
			return m.handlePinToggle()
		case "u":
			return m.handleUnpin()
		case "r":
			return m.handleRefresh()
		case "R":
			return m.handleReload()
		}

	case tickMsg:
		return m, tea.Batch(m.fetchStatusCmd(), m.tickCmd())

	case statusMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			m.status = msg.status
			if len(m.status.Accounts) > 0 {
				if m.cursor >= len(m.status.Accounts) {
					m.cursor = len(m.status.Accounts) - 1
				}
			} else {
				m.cursor = 0
			}
		}
		return m, nil

	case actionResultMsg:
		if msg.err != nil {
			m.actionMsg = fmt.Sprintf("Error %s: %v", strings.ToLower(msg.action), msg.err)
			m.actionMsgErr = true
		} else {
			if msg.message != "" {
				m.actionMsg = msg.message
			} else {
				m.actionMsg = fmt.Sprintf("%s successful", msg.action)
			}
			m.actionMsgErr = false
		}
		return m, m.fetchStatusCmd()
	}

	return m, nil
}

// View renders the TUI to a string.
func (m Model) View() string {
	if m.quitting {
		return "Detached from Gemini Bridge.\n"
	}
	return renderView(m)
}

func (m Model) handlePinToggle() (tea.Model, tea.Cmd) {
	if m.status == nil || len(m.status.Accounts) == 0 || m.cursor < 0 || m.cursor >= len(m.status.Accounts) {
		return m, nil
	}

	selected := m.status.Accounts[m.cursor]
	accountID := selected.ID
	if accountID == "" {
		accountID = selected.Email
	}

	isCurrentlyPinned := m.status.Mode == "pinned" &&
		(m.status.PinnedAccountID == selected.ID || m.status.PinnedAccountID == selected.Email)

	if isCurrentlyPinned {
		return m, func() tea.Msg {
			if m.client == nil {
				return actionResultMsg{action: "Unpin", err: errors.New("admin client nil")}
			}
			err := m.client.Unpin()
			if err != nil {
				return actionResultMsg{action: "Unpin", err: err}
			}
			return actionResultMsg{action: "Unpin", message: "Unpinned to Sticky mode"}
		}
	}

	email := selected.Email
	return m, func() tea.Msg {
		if m.client == nil {
			return actionResultMsg{action: "Pin", err: errors.New("admin client nil")}
		}
		err := m.client.PinAccount(accountID)
		if err != nil {
			return actionResultMsg{action: "Pin", err: err}
		}
		return actionResultMsg{action: "Pin", message: fmt.Sprintf("Pinned to %s", email)}
	}
}

func (m Model) handleUnpin() (tea.Model, tea.Cmd) {
	return m, func() tea.Msg {
		if m.client == nil {
			return actionResultMsg{action: "Unpin", err: errors.New("admin client nil")}
		}
		err := m.client.Unpin()
		if err != nil {
			return actionResultMsg{action: "Unpin", err: err}
		}
		return actionResultMsg{action: "Unpin", message: "Unpinned to Sticky mode"}
	}
}

func (m Model) handleRefresh() (tea.Model, tea.Cmd) {
	if m.status == nil || len(m.status.Accounts) == 0 || m.cursor < 0 || m.cursor >= len(m.status.Accounts) {
		return m, nil
	}
	selected := m.status.Accounts[m.cursor]
	accountID := selected.ID
	if accountID == "" {
		accountID = selected.Email
	}
	email := selected.Email

	return m, func() tea.Msg {
		if m.client == nil {
			return actionResultMsg{action: "Refresh", err: errors.New("admin client nil")}
		}
		err := m.client.RefreshToken(accountID)
		if err != nil {
			return actionResultMsg{action: "Refresh", err: err}
		}
		return actionResultMsg{action: "Refresh", message: fmt.Sprintf("Token refreshed for %s", email)}
	}
}

func (m Model) handleReload() (tea.Model, tea.Cmd) {
	return m, func() tea.Msg {
		if m.client == nil {
			return actionResultMsg{action: "Reload", err: errors.New("admin client nil")}
		}
		err := m.client.Reload()
		if err != nil {
			return actionResultMsg{action: "Reload", err: err}
		}
		return actionResultMsg{action: "Reload", message: "Configurations reloaded from disk"}
	}
}

// RunTUI connects to the admin server at adminURL and starts the interactive TUI program.
func RunTUI(adminURL string) error {
	client := admin.NewAdminClient(adminURL)
	m := NewModel(client)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
