package polymarket

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SecretFinding is one suspicious assignment found in the working tree. The
// matched value is never included, only its length.
type SecretFinding struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
	Size int    `json:"value_length"`
}

// EnvStatus reports whether an environment variable is present, without value.
type EnvStatus struct {
	Name   string `json:"name"`
	Set    bool   `json:"set"`
	Length int    `json:"length,omitempty"`
}

// SecretsAudit is the offline result of -secrets-audit. It makes no network
// calls and never prints a secret.
type SecretsAudit struct {
	Root             string          `json:"root"`
	FilesScanned     int             `json:"files_scanned"`
	FilesSkipped     int             `json:"files_skipped"`
	Findings         []SecretFinding `json:"findings"`
	ConfigFile       string          `json:"config_file"`
	ConfigExists     bool            `json:"config_exists"`
	ConfigHasSecrets bool            `json:"config_has_secrets"`
	ConfigIgnored    bool            `json:"config_gitignored"`
	EnvVars          []EnvStatus     `json:"env"`
	OutboundHosts    []string        `json:"outbound_hosts"`
	Verdict          string          `json:"verdict"`
	Note             string          `json:"note"`
}

var (
	// JSON keys are quoted, so allow an optional closing quote before the colon.
	privateKeyPattern = regexp.MustCompile(`(?i)(private[_-]?key)["']?\s*[:=]\s*["']?0x[0-9a-f]{64}`)
	secretPattern     = regexp.MustCompile(`(?i)(api[_-]?secret|passphrase)["']?\s*[:=]\s*["']([A-Za-z0-9+/=_\-]{12,})["']`)
)

// AuditSecrets walks root looking for committed credentials and reports which
// environment variables are present (never their values).
func AuditSecrets(root string) (*SecretsAudit, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	audit := &SecretsAudit{
		Root:     absolute,
		Findings: make([]SecretFinding, 0),
		OutboundHosts: []string{
			GammaAPIBaseURL,
			CLOBAPIBaseURL,
			DataAPIBaseURL,
			DefaultPolygonRPC,
		},
		Note: "offline audit: no network call, match values are never printed",
	}

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"data": true, "reports": true, "dist": true, "build": true,
	}
	err = filepath.WalkDir(absolute, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".test") {
			audit.FilesSkipped++
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil || info.Size() > 1<<20 {
			audit.FilesSkipped++
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil || strings.ContainsRune(string(content), 0) {
			audit.FilesSkipped++
			return nil
		}
		audit.FilesScanned++
		relative, _ := filepath.Rel(absolute, path)
		for lineNumber, line := range strings.Split(string(content), "\n") {
			if match := privateKeyPattern.FindStringSubmatchIndex(line); match != nil {
				audit.Findings = append(audit.Findings, SecretFinding{
					File: relative, Line: lineNumber + 1, Kind: "private_key", Size: match[1] - match[0],
				})
			}
			if match := secretPattern.FindStringSubmatchIndex(line); match != nil {
				audit.Findings = append(audit.Findings, SecretFinding{
					File: relative, Line: lineNumber + 1, Kind: "api_secret_or_passphrase", Size: match[5] - match[4],
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", absolute, err)
	}

	configPath := filepath.Join(absolute, "polymarket_config.json")
	audit.ConfigFile = configPath
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		audit.ConfigExists = true
		var parsed Config
		if json.Unmarshal(data, &parsed) == nil {
			audit.ConfigHasSecrets = parsed.Polymarket.APIKey != "" ||
				parsed.Polymarket.APISecret != "" ||
				parsed.Polymarket.Passphrase != "" ||
				parsed.Polymarket.PrivateKey != ""
		}
	}
	if data, readErr := os.ReadFile(filepath.Join(absolute, ".gitignore")); readErr == nil {
		text := string(data)
		audit.ConfigIgnored = strings.Contains(text, "polymarket_config.json")
	}

	for _, name := range []string{
		"POLYMARKET_API_KEY", "POLYMARKET_API_SECRET", "POLYMARKET_PASSPHRASE",
		"POLYMARKET_PRIVATE_KEY", "POLYMARKET_WALLET_ADDRESS", "POLYMARKET_FUNDER_ADDRESS",
		"POLYMARKET_ALLOW_LIVE",
	} {
		value := os.Getenv(name)
		status := EnvStatus{Name: name, Set: value != ""}
		if status.Set {
			status.Length = len(value)
		}
		audit.EnvVars = append(audit.EnvVars, status)
	}

	switch {
	case len(audit.Findings) > 0:
		audit.Verdict = "REVIEW"
	case audit.ConfigHasSecrets:
		audit.Verdict = "REVIEW"
	case !audit.ConfigIgnored:
		audit.Verdict = "REVIEW"
	default:
		audit.Verdict = "CLEAN"
	}
	return audit, nil
}
