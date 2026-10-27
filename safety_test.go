package polymarket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveOrderAllowedEnvelope(t *testing.T) {
	dir := t.TempDir()
	stopFile := filepath.Join(dir, "STOP")
	config := DefaultConfig()
	config.Trading.Mode = "live"
	config.Trading.Enabled = true
	config.Trading.AllowLive = true
	config.Trading.AllowedTokens = []string{"token-1"}
	config.Trading.StopFile = stopFile
	t.Setenv("POLYMARKET_ALLOW_LIVE", "1")

	if err := config.LiveOrderAllowed("token-1"); err != nil {
		t.Fatalf("expected allowed: %v", err)
	}
	if err := config.LiveOrderAllowed("token-2"); err == nil {
		t.Fatal("token outside whitelist must be rejected")
	}

	config.Trading.AllowedTokens = nil
	if err := config.LiveOrderAllowed("token-1"); err == nil || !strings.Contains(err.Error(), "whitelist") {
		t.Fatalf("empty whitelist must fail closed, got %v", err)
	}

	config.Trading.AllowedTokens = []string{"token-1"}
	if err := os.WriteFile(stopFile, []byte("stop"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.LiveOrderAllowed("token-1"); err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("STOP file must block orders, got %v", err)
	}
}

func TestHeartbeatAndDecisionLog(t *testing.T) {
	dir := t.TempDir()
	heartbeat := filepath.Join(dir, "heartbeat.txt")
	if err := WriteHeartbeat(heartbeat); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(heartbeat); err != nil || len(data) == 0 {
		t.Fatalf("heartbeat not written: %v", err)
	}

	logger, err := NewDecisionLogger(filepath.Join(dir, "decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Write(DecisionRecord{Type: "skip", MarketID: "m", Reason: "missing_data"}); err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"skip"`) || !strings.Contains(string(data), "missing_data") {
		t.Fatalf("unexpected decision log: %s", data)
	}
}
