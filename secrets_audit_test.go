package polymarket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAuditFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestAuditSecretsFindsCommittedCredentials(t *testing.T) {
	dir := t.TempDir()
	writeAuditFile(t, dir, "leak.txt", "private_key = 0x"+strings.Repeat("a", 64)+"\n")
	writeAuditFile(t, dir, "cfg.json", `{"polymarket":{"api_secret":"abcdefghijklmnop"}}`)
	writeAuditFile(t, dir, ".gitignore", "polymarket_config.json\n")

	audit, err := AuditSecrets(dir)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(audit.Findings) < 2 {
		t.Fatalf("expected at least two findings, got %+v", audit.Findings)
	}
	if audit.Verdict != "REVIEW" {
		t.Fatalf("expected REVIEW, got %q", audit.Verdict)
	}
	if !audit.ConfigIgnored {
		t.Fatalf("expected the config to be recognised as gitignored")
	}
	for _, finding := range audit.Findings {
		if finding.Size <= 0 {
			t.Fatalf("expected a size, got %+v", finding)
		}
	}
}

func TestAuditSecretsCleanTree(t *testing.T) {
	dir := t.TempDir()
	writeAuditFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	writeAuditFile(t, dir, ".gitignore", "polymarket_config.json\n.env\n")

	audit, err := AuditSecrets(dir)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(audit.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", audit.Findings)
	}
	if audit.Verdict != "CLEAN" {
		t.Fatalf("expected CLEAN, got %q", audit.Verdict)
	}
	if audit.FilesScanned == 0 {
		t.Fatal("expected at least one scanned file")
	}
}

func TestAuditSecretsSkipsTestFilesAndReportsEnvPresenceOnly(t *testing.T) {
	dir := t.TempDir()
	writeAuditFile(t, dir, "x_test.go", "private_key = 0x"+strings.Repeat("b", 64)+"\n")
	writeAuditFile(t, dir, ".gitignore", "polymarket_config.json\n")

	t.Setenv("POLYMARKET_API_KEY", "secret-value-123")
	audit, err := AuditSecrets(dir)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(audit.Findings) != 0 {
		t.Fatalf("test fixtures must be skipped, got %+v", audit.Findings)
	}
	var found bool
	for _, env := range audit.EnvVars {
		if env.Name == "POLYMARKET_API_KEY" {
			found = true
			if !env.Set || env.Length != len("secret-value-123") {
				t.Fatalf("unexpected env status: %+v", env)
			}
		}
	}
	if !found {
		t.Fatal("expected POLYMARKET_API_KEY in the report")
	}
}

func TestAuditSecretsFlagsConfigWithSecrets(t *testing.T) {
	dir := t.TempDir()
	writeAuditFile(t, dir, "polymarket_config.json", `{"polymarket":{"api_key":"abc","wallet_address":"0x1"}}`)
	writeAuditFile(t, dir, ".gitignore", "polymarket_config.json\n")

	audit, err := AuditSecrets(dir)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !audit.ConfigExists || !audit.ConfigHasSecrets {
		t.Fatalf("expected a config with secrets, got %+v", audit)
	}
	if audit.Verdict != "REVIEW" {
		t.Fatalf("expected REVIEW, got %q", audit.Verdict)
	}
}
