package polymarket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigFromUsesSafeDefaultsAndValidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"risk":{"total_capital":2000}}`), 0600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Trading.Mode != "paper" || config.Trading.Enabled {
		t.Fatalf("configuration did not default to disabled paper mode: %+v", config.Trading)
	}
	if config.Risk.TotalCapital != 2000 {
		t.Fatalf("custom config value was not loaded: %f", config.Risk.TotalCapital)
	}
}

func TestLoadConfigFromRejectsInvalidRiskSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"risk":{"max_drawdown":2}}`), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfigFrom(path); err == nil || !strings.Contains(err.Error(), "max_drawdown") {
		t.Fatalf("expected max_drawdown validation error, got %v", err)
	}
}

func TestLoadConfigFromReportsMissingFile(t *testing.T) {
	_, err := LoadConfigFrom(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("expected missing config file to return an error")
	}
}
