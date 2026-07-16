package http

import (
	"github.com/gin-gonic/gin"
)

// handleConfigStatus reports drift state, the configured source, the running
// vs source checksums + timestamps, the last error, the current dashboard_mode,
// and the OAuth callback base the dashboard shows users to paste into their
// provider console. The SPA renders its banners and the fixed callback URL from
// this one response, with no separate config endpoint round-trip.
func (h *AdminHandler) handleConfigStatus(c *gin.Context) {
	callbackBase := oauthCallbackBase(h.liveConfig())
	if h.driftFn == nil {
		c.JSON(200, gin.H{
			"status":              "unknown",
			"dashboard_mode":      h.dashboardMode.String(),
			"dotenv_writable":     h.dotenvWritable,
			"oauth_callback_base": callbackBase,
		})
		return
	}
	tracker := h.driftFn()
	if tracker == nil {
		c.JSON(200, gin.H{
			"status":              "unknown",
			"dashboard_mode":      h.dashboardMode.String(),
			"dotenv_writable":     h.dotenvWritable,
			"oauth_callback_base": callbackBase,
		})
		return
	}
	state := tracker.Snapshot()
	c.JSON(200, gin.H{
		"status":        state.Status,
		"config_source": state.ConfigSource,
		"running": gin.H{
			"applied_at": state.RunningAppliedAt,
			"checksum":   state.RunningChecksum,
		},
		"source": gin.H{
			"checksum":     state.SourceChecksum,
			"last_seen_at": state.SourceLastSeenAt,
		},
		"last_error":          state.LastError,
		"dashboard_mode":      h.dashboardMode.String(),
		"dotenv_writable":     h.dotenvWritable,
		"oauth_callback_base": callbackBase,
	})
}
