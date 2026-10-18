// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"fmt"
	"time"
)

// Bytes formats a size in binary units: "13 B", "2.9 MiB".
func Bytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	value, unit := float64(n)/1024, 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

// Duration formats an elapsed time by its two largest units: "1h 30m", "1m 12s", "45s".
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	hours, minutes, seconds := int(d/time.Hour), int(d%time.Hour/time.Minute), int(d%time.Minute/time.Second)
	switch {
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

// StatusIcon summarizes a check or run in one cell: ✓ succeeded, X failed, - skipped or canceled, and * still
// pending. A conclusion, when present, takes precedence over the status.
func StatusIcon(status, conclusion string) Cell {
	if conclusion != "" {
		status = conclusion
	}
	switch status {
	case "success":
		return Cell{Text: "✓", Color: "green"}
	case "failure", "error", "warning":
		return Cell{Text: "X", Color: "red"}
	case "cancelled", "canceled", "skipped": //nolint:misspell // Gitea API spelling.
		return Cell{Text: "-", Muted: true}
	default:
		return Cell{Text: "*", Color: "yellow"}
	}
}
