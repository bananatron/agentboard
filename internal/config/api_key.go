package config

import (
	"fmt"
	"os"
	"strings"
)

const apiKeyEnvVar = "AGENTBOARD_API_KEY"

// APIKey returns AGENTBOARD_API_KEY from the environment. The API server
// cannot generate or persist the key inside the Railway container, so the
// variable must be provided via deployment settings or a mounted secret.
func APIKey() (string, error) {
	key := strings.TrimSpace(os.Getenv(apiKeyEnvVar))
	if key == "" {
		return "", fmt.Errorf("%s must be set", apiKeyEnvVar)
	}
	return key, nil
}
