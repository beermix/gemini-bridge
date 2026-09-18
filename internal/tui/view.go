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

	// 3. Errors Panel (if any error requests or recent errors recorded)
	if m.status != nil && (m.status.Stats.ErrorRequests > 0 || len(m.status.Stats.RecentErrors) > 0) {
		b.WriteString(renderErrorsPanel(m.status.Stats, m.width))
		b.WriteString("\n\n")
	}

	// 4. Recent Activity Panel
	b.WriteString(renderActivityPanel(m.status.Stats, m.width))
	b.WriteString("\n\n")

	// 5. Footer
	b.WriteString(renderFooter(m.actionMsg, m.actionMsgErr))

	return b.String()
}

func renderHeader(status *admin.StatusResponse, width int) string {
	var b strings.Builder

	title := titleStyle.Render(" Gemini Bridge ")

	uptimeText := "Uptime: -"
	modeBadge := modeStickyBadge.Render("📌 Sticky")
	endpointBadge := endpointProdBadge.Render("🌐 Upstream: cloudcode-pa")
	statsLine := statsLabelStyle.Render("Requests: -")

	if status != nil {
		uptime := status.Uptime
		if uptime == "" {
			uptime = "0s"
		}
		uptimeText = fmt.Sprintf("Uptime: %s", uptime)

		activeEP := status.ActiveEndpoint
		if activeEP == "" {
			activeEP = status.Stats.ActiveEndpoint
		}
		if activeEP == "" {
			activeEP = "cloudcode-pa"
		}

		if strings.Contains(activeEP, "daily") || strings.Contains(activeEP, "fallback") {
			endpointBadge = endpointFallbackBadge.Render(fmt.Sprintf("⚠️ Upstream: %s [FALLBACK]", activeEP))
		} else {
			endpointBadge = endpointProdBadge.Render(fmt.Sprintf("🌐 Upstream: %s", activeEP))
		}

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
			modeBadge = modeStickyBadge.Render("📌 Sticky")
		}

		statsLine = fmt.Sprintf("Requests: %s Total | %s OK | %s 429s | %s Errors",
			statsValStyle.Render(fmt.Sprintf("%d", status.Stats.TotalRequests)),
			statsOKStyle.Render(fmt.Sprintf("%d", status.Stats.SuccessRequests)),
			statsWarnStyle.Render(fmt.Sprintf("%d", status.Stats.RateLimitRequests)),
			statsErrStyle.Render(fmt.Sprintf("%d", status.Stats.ErrorRequests)),
		)
	}

	topLine := fmt.Sprintf("%s   %s   %s   %s", title, modeBadge, endpointBadge, headerSubStyle.Render(uptimeText))
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

func renderErrorsPanel(stats proxy.ServerStats, width int) string {
	var b strings.Builder

	title := statsErrStyle.Render("⚠️  Recent Errors")
	countStr := headerSubStyle.Render(fmt.Sprintf(" (%d error requests)", stats.ErrorRequests))
	b.WriteString(fmt.Sprintf("%s%s\n", title, countStr))

	errors := stats.RecentErrors
	if len(errors) == 0 {
		b.WriteString(headerSubStyle.Render("  No detailed error logs recorded yet.\n"))
		return b.String()
	}

	const maxDisplayErrors = 5
	start := 0
	if len(errors) > maxDisplayErrors {
		start = len(errors) - maxDisplayErrors
	}
	recent := errors[start:]

	effectiveWidth := width
	if effectiveWidth < 100 {
		effectiveWidth = 125
	}

	for i := len(recent) - 1; i >= 0; i-- {
		entry := recent[i]
		ts := entry.Timestamp.Format("15:04:05")
		statusStyle := statsErrStyle
		if entry.Status == 429 {
			statusStyle = statsWarnStyle
		}
		statusStr := statusStyle.Render(fmt.Sprintf("%3d", entry.Status))

		modelName := entry.Model
		if entry.TargetModel != "" && entry.TargetModel != entry.Model {
			modelName = fmt.Sprintf("%s → %s", entry.Model, entry.TargetModel)
		}
		if modelName == "" {
			modelName = "-"
		}

		ep := entry.Endpoint
		if ep == "" {
			ep = "cloudcode-pa"
		}
		epStr := epProdStyle.Render(ep)
		if strings.Contains(ep, "daily") {
			epStr = epDailyStyle.Render(ep)
		}

		acc := shortAccount(entry.AccountEmail)
		if acc == "" {
			acc = "-"
		}

		headerLine := fmt.Sprintf("  [%s] %s │ %-4s │ %s │ acc: %s │ ep: %s │ %s",
			ts, statusStr, entry.Method, modelStyle.Render(truncateString(modelName, 25)), acc, epStr, entry.Path)
		b.WriteString(headerLine)
		b.WriteString("\n")

		errMsg := entry.Error
		if errMsg == "" {
			errMsg = "HTTP error response without detailed message"
		}
		maxErrLen := effectiveWidth - 10
		if maxErrLen < 50 {
			maxErrLen = 80
		}
		b.WriteString(errorDetailStyle.Render(fmt.Sprintf("    ↳ %s", truncateString(errMsg, maxErrLen))))
		b.WriteString("\n")
	}

	return b.String()
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
	// TIME (8) + METHOD (4) + STATUS (3) + DURATION (6) + EP (5) + PATH (22) + ACCOUNT (12)
	// 7 vertical separators " │ " = 21 chars + 2 leading indent = 81 fixed chars.
	// Remaining width goes to MODEL column.
	accountWidth := 12
	pathWidth := 22
	fixedWidth := 81

	modelWidth := effectiveWidth - fixedWidth
	if modelWidth < 25 {
		modelWidth = 25
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

		epStr := epProdStyle.Render("prod ")
		if strings.Contains(entry.Endpoint, "daily") {
			epStr = epDailyStyle.Render("daily")
		} else if entry.Endpoint == "" {
			epStr = headerSubStyle.Render("  -  ")
		}

		displayModel := entry.Model
		if entry.TargetModel != "" && entry.TargetModel != entry.Model {
			displayModel = fmt.Sprintf("%s → %s", entry.Model, entry.TargetModel)
		}

		modelStr := modelStyle.Render(fmt.Sprintf("%-*s", modelWidth, truncateString(displayModel, modelWidth)))
		pathStr := fmt.Sprintf("%-*s", pathWidth, truncateString(entry.Path, pathWidth))
		accountStr := fmt.Sprintf("%-*s", accountWidth, truncateString(shortAccount(entry.AccountEmail), accountWidth))

		line := fmt.Sprintf("  %s %s %s %s %s %s %s %s %s %s %s %s %s %s %s",
			ts, sep, method, sep, statusStr, sep, durStr, sep, epStr, sep, modelStr, sep, pathStr, sep, accountStr)

		if i%2 == 1 {
			line = gridRowOddStyle.Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")

		if entry.Error != "" {
			maxErrLen := effectiveWidth - 12
			if maxErrLen < 50 {
				maxErrLen = 80
			}
			b.WriteString(errorDetailStyle.Render(fmt.Sprintf("       ↳ error: %s\n", truncateString(entry.Error, maxErrLen))))
		}
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
