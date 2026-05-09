package http

import (
	"github.com/gin-gonic/gin"
)

// handleConfigStatus reports drift state, the configured source, the running
// vs source checksums + timestamps, the last error, and the current
// dashboard_mode so the SPA can render the correct banner without a
// separate config endpoint round-trip.
func (h *AdminHandler) handleConfigStatus(c *gin.Context) {
	if h.driftFn == nil {
		c.JSON(200, gin.H{
			"status":         "unknown",
			"dashboard_mode": h.dashboardMode.String(),
		})
		return
	}
	tracker := h.driftFn()
	if tracker == nil {
		c.JSON(200, gin.H{
			"status":         "unknown",
			"dashboard_mode": h.dashboardMode.String(),
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
		"last_error":     state.LastError,
		"dashboard_mode": h.dashboardMode.String(),
	})
}
