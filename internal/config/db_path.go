package config

import (
	"fmt"
	"os"
	"strings"
)

const dbPathEnvVar = "AGENTBOARD_DB_PATH"

// DatabasePath returns the SQLite path defined via AGENTBOARD_DB_PATH. Railway
// deploys mount the /data volume as read/write storage, so the caller must set
// AGENTBOARD_DB_PATH (for example /data/board.db) before starting the server.
func DatabasePath() (string, error) {
	path := strings.TrimSpace(os.Getenv(dbPathEnvVar))
	if path == "" {
		return "", fmt.Errorf("%s must be set", dbPathEnvVar)
	}
	return path, nil
}
