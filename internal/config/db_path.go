package config

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	dbPathEnvVar    = "AGENTBOARD_DB_PATH"
	defaultDBRelDir = ".agentboard"
	defaultDBFile   = "board.db"
)

// DatabasePath returns the path to the SQLite database. If AGENTBOARD_DB_PATH
// is set it takes precedence; otherwise it falls back to .agentboard/board.db.
func DatabasePath() string {
	if custom := strings.TrimSpace(os.Getenv(dbPathEnvVar)); custom != "" {
		return custom
	}
	return filepath.Join(defaultDBRelDir, defaultDBFile)
}
