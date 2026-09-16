package tui

import (
	"fmt"
	"strings"
	"time"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/admin"
	"gemini-bridge/internal/proxy"
)

func renderView(m Model) string {
	var b strings.Builder

	if m.status == nil {
		b.WriteString(renderHeader(nil, m.width))
		b.WriteString("\n\n")
		if m.err != nil {
			b.WriteString(feedbackErrorStyle.Render(fmt.Sprintf("  Error connecting to admin server: %v", m.err)))
		} else {
			b.WriteString(headerSubStyle.Render("  Loading status from admin server..."))
		}
		b.WriteString("\n\n")
		b.WriteString(renderFooter(m.actionMsg, m.actionMsgErr))
		return b.String()
	}

	// 1. Header
	b.WriteString(renderHeader(m.status, m.width))
	b.WriteString("\n\n")

	// Display connection warning if server is currently unreachable
	if m.err != nil {
		b.WriteString(feedbackErrorStyle.Render(fmt.Sprintf("  ⚠️  Connection lost: %v (showing cached status)", m.err)))
		b.WriteString("\n\n")
	}

	// 2. Accounts Table
	b.WriteString(renderAccountsTable(m.status, m.cursor, m.width))
	b.WriteString("\n\n")

	// 3. Recent Activity Panel
	b.WriteString(renderActivityPanel(m.status.Stats, m.width))
	b.WriteString("\n\n")

	// 4. Footer
	b.WriteString(renderFooter(m.actionMsg, m.actionMsgErr))

	return b.String()
}

func renderHeader(status *admin.StatusResponse, width int) string {
	var b strings.Builder

	title := titleStyle.Render(" Gemini Bridge ")

	uptimeText := "Uptime: -"
	modeBadge := modeRoundRobinBadge.Render("🔄 Round-Robin")
	statsLine := statsLabelStyle.Render("Requests: -")

	if status != nil {
		uptime := status.Uptime
		if uptime == "" {
			uptime = "0s"
		}
		uptimeText = fmt.Sprintf("Uptime: %s", uptime)

		if status.Mode == "pinned" {
			pinnedEmail := status.PinnedAccountID
			for _, acc := range status.Accounts {
				if acc.ID == status.PinnedAccountID || acc.Email == status.PinnedAccountID {
					pinnedEmail = acc.Email
					break
				}
			}
			modeBadge = modePinnedBadge.Render(fmt.Sprintf("📌 Pinned: %s", pinnedEmail))
		} else {
			modeBadge = modeRoundRobinBadge.Render("🔄 Round-Robin")
		}

		statsLine = fmt.Sprintf("Requests: %s Total | %s OK | %s 429s | %s Errors",
			statsValStyle.Render(fmt.Sprintf("%d", status.Stats.TotalRequests)),
			statsOKStyle.Render(fmt.Sprintf("%d", status.Stats.SuccessRequests)),
			statsWarnStyle.Render(fmt.Sprintf("%d", status.Stats.RateLimitRequests)),
			statsErrStyle.Render(fmt.Sprintf("%d", status.Stats.ErrorRequests)),
		)
	}

	topLine := fmt.Sprintf("%s   %s   %s", title, modeBadge, headerSubStyle.Render(uptimeText))
	b.WriteString(topLine)
	b.WriteString("\n")
	b.WriteString(statsLine)

	return b.String()
}

func renderAccountsTable(status *admin.StatusResponse, cursor int, width int) string {
	var b strings.Builder

	headerLine := fmt.Sprintf("   %-4s  %-12s  %-32s %-20s %-10s", "PIN", "STATUS", "EMAIL", "PROJECT ID", "EXPIRY")
	b.WriteString(tableHeaderStyle.Render(headerLine))
	b.WriteString("\n")

	if len(status.Accounts) == 0 {
		b.WriteString(headerSubStyle.Render("    No accounts loaded.\n"))
		return b.String()
	}

	for i, acc := range status.Accounts {
		cursorIcon := " "
		if i == cursor {
			cursorIcon = cursorActiveStyle.Render(">")
		}

		pinIcon := "  "
		if status.Mode == "pinned" && (status.PinnedAccountID == acc.ID || status.PinnedAccountID == acc.Email) {
			pinIcon = pinIconStyle.Render("📌")
		}

		badge := formatStatusBadge(acc)
		projID := acc.Token.ProjectID
		if projID == "" {
			projID = "-"
		}
		expiry := formatExpiry(acc)

		rowStr := fmt.Sprintf("  %s %s   %s   %-32s %-20s %-10s",
			cursorIcon,
			pinIcon,
			badge,
			truncateString(acc.Email, 32),
			truncateString(projID, 20),
			truncateString(expiry, 10),
		)

		if i == cursor {
			rowStr = tableRowSelectedStyle.Render(rowStr)
		} else {
			rowStr = tableRowStyle.Render(rowStr)
		}

		b.WriteString(rowStr)
		b.WriteString("\n")
	}

	return b.String()
}

func formatStatusBadge(acc *account.CloudAccount) string {
	if acc.IsCooldown() {
		return statusCooldownBadge.Render("🟡 COOLDOWN")
	}
	if acc.IsTokenExpired(0) {
		return statusExpiredBadge.Render("🔴 EXPIRED ")
	}
	if strings.EqualFold(acc.Status, "error") {
		return statusErrorBadge.Render("🔴 ERROR   ")
	}
	return statusActiveBadge.Render("🟢 ACTIVE  ")
}

func formatExpiry(acc *account.CloudAccount) string {
	if acc.Token.ExpiryTimestamp == 0 {
		return "n/a"
	}
	expTime := time.Unix(acc.Token.ExpiryTimestamp, 0)
	remain := time.Until(expTime)
	if remain <= 0 {
		return "Expired"
	}
	if remain < time.Minute {
		return fmt.Sprintf("in %ds", int(remain.Seconds()))
	}
	if remain < time.Hour {
		return fmt.Sprintf("in %dm", int(remain.Minutes()))
	}
	return fmt.Sprintf("in %dh%02dm", int(remain.Hours()), int(remain.Minutes())%60)
}

func renderActivityPanel(stats proxy.ServerStats, width int) string {
	var b strings.Builder

	title := panelTitleStyle.Render("Recent Activity")
	b.WriteString(fmt.Sprintf("%s\n", title))

	logs := stats.RecentLogs
	if len(logs) == 0 {
		b.WriteString(headerSubStyle.Render("  No recent requests.\n"))
		return b.String()
	}

	const maxDisplayLogs = 35
	start := 0
	if len(logs) > maxDisplayLogs {
		start = len(logs) - maxDisplayLogs
	}
	recent := logs[start:]

	effectiveWidth := width
	if effectiveWidth < 100 {
		effectiveWidth = 125
	}

	// Dynamic column layout:
	// TIME (8) + METHOD (4) + STATUS (3) + DURATION (6) + PATH (24) + ACCOUNT (12)
	// 6 vertical separators " │ " = 18 chars + 2 leading indent = 77 fixed chars.
	// Remaining width goes to MODEL column.
	accountWidth := 12
	pathWidth := 24
	fixedWidth := 77

	modelWidth := effectiveWidth - fixedWidth
	if modelWidth < 30 {
		modelWidth = 30
	}

	sep := gridSepStyle.Render("│")

	for i, entry := range recent {
		ts := entry.Timestamp.Format("15:04:05")
		method := methodStyle.Render(fmt.Sprintf("%-4s", entry.Method))

		statusStyle := statsOKStyle
		if entry.Status >= 500 {
			statusStyle = statsErrStyle
		} else if entry.Status >= 400 {
			statusStyle = statsWarnStyle
		}
		statusStr := statusStyle.Render(fmt.Sprintf("%3d", entry.Status))

		durStr := fmt.Sprintf("%-6s", formatDuration(entry.Duration))

		displayModel := entry.Model
		if entry.TargetModel != "" && entry.TargetModel != entry.Model {
			displayModel = fmt.Sprintf("%s → %s", entry.Model, entry.TargetModel)
		}

		modelStr := modelStyle.Render(fmt.Sprintf("%-*s", modelWidth, truncateString(displayModel, modelWidth)))
		pathStr := fmt.Sprintf("%-*s", pathWidth, truncateString(entry.Path, pathWidth))
		accountStr := fmt.Sprintf("%-*s", accountWidth, truncateString(shortAccount(entry.AccountEmail), accountWidth))

		line := fmt.Sprintf("  %s %s %s %s %s %s %s %s %s %s %s %s %s",
			ts, sep, method, sep, statusStr, sep, durStr, sep, modelStr, sep, pathStr, sep, accountStr)

		if i%2 == 1 {
			line = gridRowOddStyle.Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func shortAccount(email string) string {
	if idx := strings.Index(email, "@"); idx != -1 {
		return email[:idx]
	}
	return email
}

func renderFooter(actionMsg string, isErr bool) string {
	var b strings.Builder

	if actionMsg != "" {
		if isErr {
			b.WriteString(feedbackErrorStyle.Render("  ! " + actionMsg))
		} else {
			b.WriteString(feedbackSuccessStyle.Render("  ✓ " + actionMsg))
		}
		b.WriteString("\n")
	}

	b.WriteString(helpFooterStyle.Render("  [↑/↓/j/k] Navigate  [p/Enter] Pin/Unpin  [u] Unpin  [r] Refresh Token  [R] Reload Configs  [q] Detach"))
	return b.String()
}

func truncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
