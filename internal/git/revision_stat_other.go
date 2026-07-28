//go:build !darwin && !linux

package git

import (
	"os"
	"time"
)

// Canopy supports macOS and Linux. Keeping this fallback lets other targets compile while safely
// disabling cache hits where the filesystem change time is not available through this package.
func metadataChangeTime(os.FileInfo) (time.Time, bool) {
	return time.Time{}, false
}
