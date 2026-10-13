// Package home locates the directory Convia keeps its files in (ADR 0016).
package home

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// Override names another directory, so that two instances can run side by side in development.
const Override = "CONVIA_HOME"

// Dir is %LocalAppData%\Convia, or the directory named by Override, created if missing.
func Dir() (string, error) {
	dir := os.Getenv(Override)
	if dir == "" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", errors.New("home: LOCALAPPDATA is not set")
		}
		dir = filepath.Join(local, "Convia")
	}

	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	return dir, os.MkdirAll(dir, 0o700)
}

// InstanceID is the same for every process using dir, and differs between directories.
func InstanceID(dir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return "convia-" + hex.EncodeToString(sum[:8])
}
