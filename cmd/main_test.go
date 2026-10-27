package main

import (
	"strings"
	"testing"

	"github.com/ChainOpera-Network/nofx/polymarket"
)

func TestRunBotFailsClosed(t *testing.T) {
	config := polymarket.DefaultConfig()
	if err := runBot(config); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("paper mode should fail with an explicit not-implemented error, got %v", err)
	}

	config.Trading.Mode = "live"
	if err := runBot(config); err == nil || !strings.Contains(err.Error(), "deliberately disabled") {
		t.Fatalf("live mode should be explicitly refused, got %v", err)
	}

	config.Trading.Mode = "paper"
	config.Trading.Enabled = true
	if err := runBot(config); err == nil || !strings.Contains(err.Error(), "deliberately disabled") {
		t.Fatalf("enabled trading should be explicitly refused, got %v", err)
	}
}
