package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joho/godotenv"
)

const (
	apiKeyEnvVar = "AGENTBOARD_API_KEY"
	envFileName  = ".env"
)

// EnsureAPIKey loads AGENTBOARD_API_KEY from the environment or .env file.
// If the key does not exist it generates a new 256-bit key, writes it to .env,
// and returns the generated value.
func EnsureAPIKey() (string, error) {
	// Load .env if present, ignore error if missing.
	_ = godotenv.Load()

	key := strings.TrimSpace(os.Getenv(apiKeyEnvVar))
	if key != "" {
		return key, nil
	}

	newKey, err := generateAPIKey()
	if err != nil {
		return "", fmt.Errorf("generating API key: %w", err)
	}

	if err := persistAPIKey(newKey); err != nil {
		return "", err
	}

	// Make sure future lookups during this process see the generated key.
	if err := os.Setenv(apiKeyEnvVar, newKey); err != nil {
		return "", fmt.Errorf("setting %s: %w", apiKeyEnvVar, err)
	}
	return newKey, nil
}

func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func persistAPIKey(key string) error {
	envPath := filepath.Join(".", envFileName)

	data, err := os.ReadFile(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			line := fmt.Sprintf("%s=%s\n", apiKeyEnvVar, key)
			return os.WriteFile(envPath, []byte(line), 0o600)
		}
		return fmt.Errorf("reading %s: %w", envPath, err)
	}

	entry := []byte(fmt.Sprintf("%s=%s", apiKeyEnvVar, key))
	keyPattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(apiKeyEnvVar) + `=.*$`)
	var newContent []byte

	if keyPattern.Match(data) {
		newContent = keyPattern.ReplaceAllLiteral(data, entry)
	} else {
		newContent = data
		if len(newContent) == 0 || newContent[len(newContent)-1] != '\n' {
			newContent = append(newContent, '\n')
		}
		newContent = append(newContent, append(entry, '\n')...)
	}

	// Preserve original line endings if file used CRLF.
	if bytes.Contains(data, []byte("\r\n")) {
		newContent = bytes.ReplaceAll(newContent, []byte("\n"), []byte("\r\n"))
	}

	if err := os.WriteFile(envPath, newContent, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", envPath, err)
	}
	return nil
}
