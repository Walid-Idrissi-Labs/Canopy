// Package screens keeps the frames the golden tests draw, in colour, for the demo images the
// README shows. The goldens compare text with the colour stripped; these are the same frames with
// it left in, written only when CANOPY_SCREENS_DIR names a directory, so the pictures come from the
// same fixtures the tests check and cannot drift from what the program draws.
package screens

import (
	"os"
	"path/filepath"
)

// DirEnvVar names the directory frames are written to.
const DirEnvVar = "CANOPY_SCREENS_DIR"

// Keep writes a frame as package-name.ansi when DirEnvVar is set, and does nothing otherwise. The
// package that drew it leads the name, since three packages write into one directory at once and
// two screens of the same name would otherwise overwrite each other without a word.
func Keep(pkg, name, frame string) error {
	dir := os.Getenv(DirEnvVar)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, pkg+"-"+name+".ansi"), []byte(frame), 0o644)
}
