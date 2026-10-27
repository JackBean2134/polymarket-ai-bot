package polymarket

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// CheckStopFile implements the article's kill switch: if the file exists, the
// trading loop/live executor must stop issuing new orders.
func CheckStopFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("kill switch is active: %s exists", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check stop file: %w", err)
	}
	return nil
}

func WriteHeartbeat(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	content := time.Now().UTC().Format(time.RFC3339Nano) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("write heartbeat: %w", err)
	}
	return nil
}

func containsToken(tokens []string, token string) bool {
	token = strings.TrimSpace(token)
	for _, candidate := range tokens {
		if strings.EqualFold(strings.TrimSpace(candidate), token) {
			return true
		}
	}
	return false
}
