package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Theme Colors
	colorPrimary   = lipgloss.Color("#7D56F4") // Vivid Purple
	colorSecondary = lipgloss.Color("#04B575") // Vibrant Green
	colorWarning   = lipgloss.Color("#FFB800") // Warm Yellow
	colorDanger    = lipgloss.Color("#FF4C4C") // Coral Red
	colorSubtle    = lipgloss.Color("#555566") // Slate Grey
	colorMuted     = lipgloss.Color("#888899") // Light Grey
	colorHighlight = lipgloss.Color("#00D8F6") // Bright Cyan
	colorBgSelect  = lipgloss.Color("#2E2B44") // Dark purple selection background

	// Header Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorPrimary).
			Padding(0, 1)

	headerSubStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	statsLabelStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	statsValStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF"))

	statsOKStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	statsWarnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWarning)

	statsErrStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorDanger)

	// Mode Badges
	modeStickyBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorHighlight).
				Background(lipgloss.Color("#0A2540")).
				Padding(0, 1)

	modeRoundRobinBadge = modeStickyBadge

	modePinnedBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWarning).
			Background(lipgloss.Color("#3A2E00")).
			Padding(0, 1)

	// Endpoint Badges
	endpointProdBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorSecondary).
				Background(lipgloss.Color("#0A2E20")).
				Padding(0, 1)

	endpointFallbackBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorWarning).
				Background(lipgloss.Color("#3A2A00")).
				Padding(0, 1)

	epProdStyle = lipgloss.NewStyle().
				Foreground(colorSecondary)

	epDailyStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorWarning)

	errorDetailStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF8080"))

	errorBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorDanger).
				Padding(0, 1)

	// Status Badges
	statusActiveBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorSecondary)

	statusCooldownBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorWarning)

	statusExpiredBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorDanger)

	statusErrorBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorDanger)

	// Table Styles
	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorHighlight)

	tableRowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EEEEEE"))

	tableRowSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(colorBgSelect)

	cursorActiveStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorHighlight)

	pinIconStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWarning)

	// Panel & Border Styles
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorSubtle).
			Padding(0, 1)

	panelTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	// Activity Styles
	activityLogStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#CCCCCC"))

	methodStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorHighlight)

	modelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#B088FF"))

	// Subtle Grid Styles
	colorGrid = lipgloss.Color("#444455")

	gridSepStyle = lipgloss.NewStyle().
			Foreground(colorGrid)

	gridRowOddStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#141320"))

	// Footer Styles
	helpFooterStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			PaddingTop(1)
	feedbackSuccessStyle = lipgloss.NewStyle().
				Foreground(colorSecondary).
				Bold(true)

	feedbackErrorStyle = lipgloss.NewStyle().
				Foreground(colorDanger).
				Bold(true)
)
