package polymarket

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStopFile(path string) error {
	return os.WriteFile(path, []byte("stop\n"), 0600)
}

const testSignerKey = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

func verifyTestConfig(t *testing.T) *Config {
	t.Helper()
	config := DefaultConfig()
	config.Trading.StopFile = filepath.Join(t.TempDir(), "STOP")
	return config
}

func findCheck(checks []VerifyCheck, name string) (VerifyCheck, bool) {
	for _, check := range checks {
		if check.Name == name {
			return check, true
		}
	}
	return VerifyCheck{}, false
}

func TestVerifyConfigGatesReportDisabledLive(t *testing.T) {
	config := verifyTestConfig(t)
	checks := verifyConfigGates(config, false, "token-1")

	for _, name := range []string{"live_switch", "allow_live_config", "allow_live_env", "token_whitelist"} {
		check, ok := findCheck(checks, name)
		if !ok {
			t.Fatalf("missing check %q", name)
		}
		if check.Status != VerifyWarn {
			t.Fatalf("%s should warn while live is disabled, got %s (%s)", name, check.Status, check.Detail)
		}
	}
	stop, _ := findCheck(checks, "stop_file")
	if stop.Status != VerifyPass {
		t.Fatalf("missing STOP file should pass, got %s", stop.Status)
	}
}

func TestVerifyConfigGatesFailOnWhitelistMismatchAndStopFile(t *testing.T) {
	config := verifyTestConfig(t)
	config.Trading.AllowedTokens = []string{"other-token"}
	checks := verifyConfigGates(config, true, "token-1")
	whitelist, _ := findCheck(checks, "token_whitelist")
	if whitelist.Status != VerifyFail {
		t.Fatalf("expected a whitelist failure, got %s", whitelist.Status)
	}

	config = verifyTestConfig(t)
	if err := writeStopFile(config.Trading.StopFile); err != nil {
		t.Fatalf("write stop file: %v", err)
	}
	checks = verifyConfigGates(config, true, "token-1")
	stop, _ := findCheck(checks, "stop_file")
	if stop.Status != VerifyFail {
		t.Fatalf("expected the kill switch to fail, got %s", stop.Status)
	}
}

func TestVerifyConfigGatesPassWhenFullyEnabled(t *testing.T) {
	config := verifyTestConfig(t)
	config.Trading.Mode = "live"
	config.Trading.Enabled = true
	config.Trading.AllowLive = true
	config.Trading.AllowedTokens = []string{"token-1"}

	checks := verifyConfigGates(config, true, "token-1")
	for _, check := range checks {
		if check.Status != VerifyPass {
			t.Fatalf("%s should pass, got %s (%s)", check.Name, check.Status, check.Detail)
		}
	}
}

func TestVerifyIdentityWalletModel(t *testing.T) {
	signer, err := NewOrderSigner(OrderSignerConfig{PrivateKeyHex: testSignerKey, ChainID: ChainPolygon})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	config := verifyTestConfig(t)
	config.Polymarket.WalletAddress = signer.Address()
	client := NewClient(ClientConfig{
		APIKey: "key", APISecret: "c2VjcmV0", Passphrase: "pass", WalletAddress: signer.Address(),
	})

	checks := verifyIdentity(config, client, signer)
	for _, name := range []string{"credentials.private_key", "signer.poly_address", "wallet.model"} {
		check, ok := findCheck(checks, name)
		if !ok || check.Status != VerifyPass {
			t.Fatalf("%s should pass, got %+v", name, check)
		}
	}
	if l2 := verifyL2Credentials(client); l2.Status != VerifyPass {
		t.Fatalf("complete credentials should pass, got %+v", l2)
	}

	config.Polymarket.WalletAddress = "0x0000000000000000000000000000000000000001"
	checks = verifyIdentity(config, client, signer)
	if mismatch, _ := findCheck(checks, "signer.poly_address"); mismatch.Status != VerifyFail {
		t.Fatalf("expected a signer mismatch failure, got %s", mismatch.Status)
	}

	config.Polymarket.WalletAddress = signer.Address()
	config.Polymarket.SignatureType = 1
	checks = verifyIdentity(config, client, signer)
	if model, _ := findCheck(checks, "wallet.model"); model.Status != VerifyFail {
		t.Fatalf("proxy signature type without a funder must fail, got %s", model.Status)
	}

	noCreds := NewClient(ClientConfig{})
	if l2 := verifyL2Credentials(noCreds); l2.Status != VerifyFail {
		t.Fatalf("missing L2 credentials must fail, got %+v", l2)
	}
}

func TestFeeChecksUseScheduleNotLegacyBaseFee(t *testing.T) {
	market := &Market{FeeRateKnown: true, FeeRate: 0.05, FeeExponent: 1, TakerBaseFee: 1000}
	check := feeCheck(market, 100, 0.5)
	if check.Status != VerifyPass || !strings.Contains(check.Detail, "1.2500") {
		t.Fatalf("unexpected fee check: %+v", check)
	}

	legacy := feeLegacyCheck(market, 100, 0.5)
	if legacy.Status != VerifyWarn {
		t.Fatalf("legacy base fee should warn, got %+v", legacy)
	}

	unknown := &Market{TakerBaseFee: 1000}
	if got := feeCheck(unknown, 100, 0.5); got.Status != VerifyFail {
		t.Fatalf("unknown schedule must fail, got %+v", got)
	}
	free := &Market{FeeRateKnown: true, FeeRate: 0}
	if got := feeCheck(free, 100, 0.5); got.Status != VerifyPass || !strings.Contains(got.Detail, "0.0000") {
		t.Fatalf("fee-free market must pass with zero fee, got %+v", got)
	}
}

func TestVerifyAllowanceStates(t *testing.T) {
	config := verifyTestConfig(t)
	config.Polymarket.WalletAddress = "0x00000000000000000000000000000000000000aa"
	market := &Market{FeeRateKnown: true, FeeRate: 0.05, FeeExponent: 1}
	request := OrderRequest{Shares: 100, LimitPrice: 0.5}

	// No checker configured: skipped, not failed.
	report := &LiveVerifyReport{}
	verifyAllowance(context.Background(), report, market, request, 0.5, "BUY", LiveVerifyDeps{Config: config})
	if len(report.Checks) != 1 || report.Checks[0].Status != VerifySkip {
		t.Fatalf("expected a skip without an RPC, got %+v", report.Checks)
	}

	// Insufficient USDC: blocker; CTF approval missing: warning.
	deps := LiveVerifyDeps{Config: config, Allowances: func(ctx context.Context, chainID int64, owner, exchange string, requiredUSD float64) (*AllowanceResult, error) {
		return &AllowanceResult{
			USDCAllowance: 1, USDCBalance: 1, RequiredUSD: requiredUSD,
			USDCSufficient: false, BalanceEnough: false, CTFApproved: false, Exchange: exchange,
		}, nil
	}}
	report = &LiveVerifyReport{}
	verifyAllowance(context.Background(), report, market, request, 0.5, "BUY", deps)
	if len(report.Checks) != 3 {
		t.Fatalf("expected three allowance checks, got %+v", report.Checks)
	}
	if report.Checks[0].Status != VerifyFail || report.Checks[0].Group != "config" {
		t.Fatalf("insufficient USDC allowance must block, got %+v", report.Checks[0])
	}
	if report.Checks[1].Name != "balance.usdc" || report.Checks[1].Status != VerifyFail {
		t.Fatalf("insufficient balance must block, got %+v", report.Checks[1])
	}
	if report.Checks[2].Status != VerifyWarn {
		t.Fatalf("missing CTF approval must warn, got %+v", report.Checks[2])
	}
	report.finish()
	if report.ConfigReady {
		t.Fatal("an allowance failure must make config_ready false")
	}

	// Sufficient and approved: both pass.
	deps.Allowances = func(ctx context.Context, chainID int64, owner, exchange string, requiredUSD float64) (*AllowanceResult, error) {
		return &AllowanceResult{
			USDCAllowance: 100, USDCBalance: 100, RequiredUSD: requiredUSD,
			USDCSufficient: true, BalanceEnough: true, CTFApproved: true,
		}, nil
	}
	report = &LiveVerifyReport{}
	verifyAllowance(context.Background(), report, market, request, 0.5, "BUY", deps)
	for _, check := range report.Checks {
		if check.Status != VerifyPass {
			t.Fatalf("expected all allowance checks to pass, got %+v", check)
		}
	}

	// RPC failure is a warning, not a blocker.
	deps.Allowances = func(ctx context.Context, chainID int64, owner, exchange string, requiredUSD float64) (*AllowanceResult, error) {
		return nil, errors.New("rpc down")
	}
	report = &LiveVerifyReport{}
	verifyAllowance(context.Background(), report, market, request, 0.5, "BUY", deps)
	if len(report.Checks) != 1 || report.Checks[0].Status != VerifyWarn {
		t.Fatalf("rpc errors should warn, got %+v", report.Checks)
	}

	// A SELL ignores USDC and blocks on missing CTF approval instead.
	deps.Allowances = func(ctx context.Context, chainID int64, owner, exchange string, requiredUSD float64) (*AllowanceResult, error) {
		return &AllowanceResult{USDCAllowance: 0, USDCBalance: 0, RequiredUSD: requiredUSD, USDCSufficient: false, BalanceEnough: false, CTFApproved: false}, nil
	}
	report = &LiveVerifyReport{}
	verifyAllowance(context.Background(), report, market, request, 0.5, "SELL", deps)
	if len(report.Checks) != 3 {
		t.Fatalf("expected three checks on a SELL, got %+v", report.Checks)
	}
	if report.Checks[0].Name != "allowance.usdc" || report.Checks[0].Status != VerifySkip {
		t.Fatalf("USDC allowance is irrelevant for a SELL, got %+v", report.Checks[0])
	}
	if report.Checks[1].Name != "balance.usdc" || report.Checks[1].Status != VerifySkip {
		t.Fatalf("USDC balance is irrelevant for a SELL, got %+v", report.Checks[1])
	}
	if report.Checks[2].Name != "allowance.ctf" || report.Checks[2].Status != VerifyFail {
		t.Fatalf("missing CTF approval must block a SELL, got %+v", report.Checks[2])
	}
}

func TestVerifyPositionStates(t *testing.T) {
	config := verifyTestConfig(t)
	config.Polymarket.WalletAddress = "0x00000000000000000000000000000000000000aa"
	request := OrderRequest{TokenID: "123", Shares: 5}

	report := &LiveVerifyReport{}
	verifyPosition(context.Background(), report, request, "BUY", LiveVerifyDeps{Config: config})
	if len(report.Checks) != 1 || report.Checks[0].Status != VerifySkip {
		t.Fatalf("expected a skip without an RPC, got %+v", report.Checks)
	}

	deps := LiveVerifyDeps{Config: config, Positions: func(ctx context.Context, chainID int64, owner, tokenID string) (float64, error) {
		return 2, nil
	}}
	report = &LiveVerifyReport{}
	verifyPosition(context.Background(), report, request, "SELL", deps)
	if report.Checks[0].Status != VerifyFail {
		t.Fatalf("selling more than held must fail, got %+v", report.Checks[0])
	}
	report.finish()
	if report.ConfigReady {
		t.Fatal("a position shortfall must block readiness")
	}

	deps.Positions = func(ctx context.Context, chainID int64, owner, tokenID string) (float64, error) {
		return 10, nil
	}
	report = &LiveVerifyReport{}
	verifyPosition(context.Background(), report, request, "SELL", deps)
	if report.Checks[0].Status != VerifyPass {
		t.Fatalf("sufficient position must pass, got %+v", report.Checks[0])
	}

	report = &LiveVerifyReport{}
	verifyPosition(context.Background(), report, request, "BUY", deps)
	if report.Checks[0].Status != VerifyPass || !strings.Contains(report.Checks[0].Detail, "informational") {
		t.Fatalf("a BUY position check is informational, got %+v", report.Checks[0])
	}

	deps.Positions = func(ctx context.Context, chainID int64, owner, tokenID string) (float64, error) {
		return 0, errors.New("rpc down")
	}
	report = &LiveVerifyReport{}
	verifyPosition(context.Background(), report, request, "SELL", deps)
	if report.Checks[0].Status != VerifyWarn {
		t.Fatalf("rpc errors should warn, got %+v", report.Checks[0])
	}
}

func TestLiveVerifyReportReadiness(t *testing.T) {
	report := &LiveVerifyReport{Checks: []VerifyCheck{
		{Group: "config", Name: "stop_file", Status: VerifyPass},
		{Group: "market", Name: "market.tradable", Status: VerifyPass},
	}}
	report.finish()
	if !report.Ready || !report.ConfigReady || !report.MarketReady {
		t.Fatalf("expected a ready report, got %+v", report)
	}

	report = &LiveVerifyReport{Checks: []VerifyCheck{
		{Group: "config", Name: "token_whitelist", Status: VerifyWarn, Detail: "empty"},
		{Group: "market", Name: "market.tradable", Status: VerifyPass},
	}}
	report.finish()
	if report.Ready || report.ConfigReady {
		t.Fatalf("warnings must block readiness: %+v", report)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("expected one warning, got %v", report.Warnings)
	}

	report = &LiveVerifyReport{Checks: []VerifyCheck{
		{Group: "market", Name: "book.depth", Status: VerifyFail, Detail: "thin"},
	}}
	report.finish()
	if report.Ready || report.MarketReady {
		t.Fatalf("market failures must block readiness: %+v", report)
	}
	if len(report.Blockers) != 1 {
		t.Fatalf("expected one blocker, got %v", report.Blockers)
	}
}

func TestCumulativeSizeAtLimitAndBestBid(t *testing.T) {
	book := &OrderBook{
		Asks: []Order{{Price: 0.40, Size: 30}, {Price: 0.42, Size: 50}, {Price: 0.50, Size: 100}},
		Bids: []Order{{Price: 0.30, Size: 10}, {Price: 0.38, Size: 25}},
	}
	if got := cumulativeSizeAtLimit(book, "BUY", 0.42); got != 80 {
		t.Fatalf("expected 80 shares at or below 0.42, got %f", got)
	}
	if got := cumulativeSizeAtLimit(book, "SELL", 0.30); got != 35 {
		t.Fatalf("expected 35 shares at or above 0.30, got %f", got)
	}
	if got := cumulativeSizeAtLimit(book, "BUY", 0); got != 0 {
		t.Fatalf("invalid limit must return 0, got %f", got)
	}
	price, _, ok := bestBid(book)
	if !ok || price != 0.38 {
		t.Fatalf("unexpected best bid: %f %v", price, ok)
	}
}
