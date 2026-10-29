package polymarket

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeBasketScanner struct {
	result  *ArbitrageScanResult
	err     error
	results []*ArbitrageScanResult
	calls   int
}

func (f *fakeBasketScanner) ScanEvents(ctx context.Context, limit int, minEdgeUSD float64) (*ArbitrageScanResult, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.results) > 0 {
		index := f.calls - 1
		if index >= len(f.results) {
			index = len(f.results) - 1
		}
		return f.results[index], nil
	}
	return f.result, nil
}

type fakeExecutor struct {
	fills   map[string]*ExecutionFill
	errs    map[string]error
	partial map[string]float64
	paper   bool
	calls   int
}

func (f *fakeExecutor) Execute(ctx context.Context, request OrderRequest) (*ExecutionFill, error) {
	f.calls++
	if err, ok := f.errs[request.TokenID]; ok && err != nil {
		return nil, err
	}
	if fill, ok := f.fills[request.TokenID]; ok {
		return fill, nil
	}
	if ratio, ok := f.partial[request.TokenID]; ok {
		shares := request.Shares * ratio
		return &ExecutionFill{
			Status:       "PAPER_PARTIAL_FILLED",
			FilledShares: shares,
			AveragePrice: request.LimitPrice,
			NotionalUSD:  shares * request.LimitPrice,
			CashUSD:      -(shares * request.LimitPrice),
		}, nil
	}
	notional := request.Shares * request.LimitPrice
	return &ExecutionFill{
		Status:       "PAPER_FILLED",
		FilledShares: request.Shares,
		AveragePrice: request.LimitPrice,
		NotionalUSD:  notional,
		CashUSD:      -notional,
	}, nil
}

func (f *fakeExecutor) Cancel(ctx context.Context, orderID string) error { return nil }

func (f *fakeExecutor) IsPaper() bool { return f.paper }

func binaryOpportunity(eventID string, yesAsk, noAsk float64, maxBaskets float64) ArbitrageOpportunity {
	return ArbitrageOpportunity{
		EventID:    eventID,
		Title:      "test event",
		Kind:       "BINARY_YES_NO",
		CostUSD:    yesAsk + noAsk,
		PayoutUSD:  1,
		NetEdgeUSD: 1 - yesAsk - noAsk,
		MaxBaskets: maxBaskets,
		Legs: []ArbitrageLeg{
			{MarketID: eventID + "-m", Outcome: "YES", TokenID: eventID + "-yes", Ask: yesAsk, AvailableShares: maxBaskets, FeeBps: 0},
			{MarketID: eventID + "-m", Outcome: "NO", TokenID: eventID + "-no", Ask: noAsk, AvailableShares: maxBaskets, FeeBps: 0},
		},
	}
}

func scanWith(opps ...ArbitrageOpportunity) *fakeBasketScanner {
	return &fakeBasketScanner{result: &ArbitrageScanResult{
		Stats:         ArbitrageScanStats{EventsScanned: 1, Opportunities: len(opps)},
		Opportunities: opps,
	}}
}

func testAutopilotConfig() AutopilotConfig {
	return AutopilotConfig{
		ScanEvents:             5,
		Interval:               time.Millisecond,
		CycleTimeout:           time.Second,
		MinNetEdgeUSD:          0.01,
		MaxPerBasketUSD:        25,
		MaxTotalUSD:            100,
		MaxPerEventUSD:         100,
		MaxUnhedgedUSD:         50,
		MaxCycles:              1,
		MaxConsecutiveFailures: 10,
		EventCooldown:          time.Hour,
	}
}

func TestAutopilotFillsCompleteBasket(t *testing.T) {
	opp := binaryOpportunity("e1", 0.54, 0.44, 100)
	opp.Category = "Sports"
	executor := &fakeExecutor{paper: true}
	bot, err := NewAutopilot(scanWith(opp), executor, testAutopilotConfig())
	if err != nil {
		t.Fatalf("new autopilot: %v", err)
	}

	report := bot.Run(context.Background())
	if report.HaltReason != "max_cycles" {
		t.Fatalf("unexpected halt reason %q", report.HaltReason)
	}
	state := report.State
	if state.BasketsFilled != 1 || state.BasketsAborted != 0 {
		t.Fatalf("unexpected basket counts: %+v", state)
	}
	// 25 baskets capped by the $25 per-basket limit at a $0.98 cost.
	if state.SharesFilled != 25 {
		t.Fatalf("expected 25 shares, got %f", state.SharesFilled)
	}
	if math.Abs(state.CashSpentUSD-24.5) > 1e-6 {
		t.Fatalf("expected $24.50 deployed, got %f", state.CashSpentUSD)
	}
	if math.Abs(state.LockedProfitUSD-0.5) > 1e-6 {
		t.Fatalf("expected $0.50 locked edge, got %f", state.LockedProfitUSD)
	}
	if len(report.Cycles) != 1 || len(report.Cycles[0].Filled) != 1 {
		t.Fatalf("expected one filled cycle, got %+v", report.Cycles)
	}
	candidates := report.Cycles[0].Candidates
	if len(candidates) != 1 {
		t.Fatalf("expected one logged candidate, got %+v", candidates)
	}
	if math.Abs(candidates[0].NetEdgeUSD-0.02) > 1e-9 || candidates[0].EventID != "e1" || candidates[0].MaxBaskets != 100 {
		t.Fatalf("unexpected candidate: %+v", candidates[0])
	}
	if candidates[0].Category != "Sports" || report.Cycles[0].Filled[0].Category != "Sports" {
		t.Fatalf("category must be propagated to candidates and baskets: %+v", report.Cycles[0])
	}
}

func TestAutopilotRejectsPartialLegAsNakedRisk(t *testing.T) {
	opp := binaryOpportunity("e1", 0.54, 0.44, 100)
	executor := &fakeExecutor{paper: true, partial: map[string]float64{"e1-no": 0.5}}
	bot, err := NewAutopilot(scanWith(opp), executor, testAutopilotConfig())
	if err != nil {
		t.Fatalf("new autopilot: %v", err)
	}

	report := bot.Run(context.Background())
	state := report.State
	if state.BasketsFilled != 0 || state.BasketsAborted != 1 {
		t.Fatalf("unexpected counts: %+v", state)
	}
	// The YES leg filled $13.50 and the NO leg filled half of $11.00, so the
	// unhedged exposure is $19.00 and is conservatively treated as a total loss.
	if math.Abs(state.NakedLegsUSD-19.0) > 1e-6 {
		t.Fatalf("expected $19.00 naked notional, got %f", state.NakedLegsUSD)
	}
	if math.Abs(state.AssumedLossUSD-19.0) > 1e-6 {
		t.Fatalf("expected $19.00 assumed loss, got %f", state.AssumedLossUSD)
	}
	if state.LockedProfitUSD != 0 {
		t.Fatalf("a partial basket must not lock profit, got %f", state.LockedProfitUSD)
	}
	if len(report.Cycles[0].Aborted) != 1 || report.Cycles[0].Aborted[0].Reason != "leg_partial_fill" {
		t.Fatalf("unexpected abort record: %+v", report.Cycles[0].Aborted)
	}
}

func TestAutopilotRefusesLiveExecutor(t *testing.T) {
	executor := &fakeExecutor{paper: false}
	if _, err := NewAutopilot(scanWith(), executor, testAutopilotConfig()); err == nil {
		t.Fatal("expected live executors to be rejected")
	}
}

func TestAutopilotRejectsDustEdgeFloor(t *testing.T) {
	config := testAutopilotConfig()
	config.MinNetEdgeUSD = 0.0001
	if _, err := NewAutopilot(scanWith(), &fakeExecutor{paper: true}, config); err == nil {
		t.Fatal("expected a sub-0.001 edge floor to be rejected")
	}
}

func TestAutopilotStopFileHaltsBeforeTrading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "STOP")
	if err := os.WriteFile(path, []byte("stop\n"), 0600); err != nil {
		t.Fatalf("write stop file: %v", err)
	}
	config := testAutopilotConfig()
	config.StopFile = path
	config.MaxCycles = 5

	bot, err := NewAutopilot(scanWith(binaryOpportunity("e1", 0.54, 0.44, 100)), &fakeExecutor{paper: true}, config)
	if err != nil {
		t.Fatalf("new autopilot: %v", err)
	}
	report := bot.Run(context.Background())
	if report.HaltReason != "stop_file" {
		t.Fatalf("expected stop_file halt, got %q", report.HaltReason)
	}
	if report.State.Cycles != 0 || report.State.BasketsFilled != 0 {
		t.Fatalf("kill switch must run before any cycle: %+v", report.State)
	}
}

func TestAutopilotEventCooldownPreventsRepeatBuy(t *testing.T) {
	config := testAutopilotConfig()
	config.MaxCycles = 3
	config.EventCooldown = time.Hour
	config.Interval = time.Millisecond

	scanner := scanWith(binaryOpportunity("e1", 0.54, 0.44, 100))
	bot, err := NewAutopilot(scanner, &fakeExecutor{paper: true}, config)
	if err != nil {
		t.Fatalf("new autopilot: %v", err)
	}
	report := bot.Run(context.Background())
	if report.State.BasketsFilled != 1 {
		t.Fatalf("cooldown should allow exactly one basket, got %+v", report.State)
	}
	if scanner.calls != 3 {
		t.Fatalf("expected 3 scan cycles, got %d", scanner.calls)
	}
}

func TestAutopilotHaltsAfterConsecutiveScanFailures(t *testing.T) {
	config := testAutopilotConfig()
	config.MaxCycles = 5
	config.MaxConsecutiveFailures = 2
	scanner := &fakeBasketScanner{err: errors.New("gamma unavailable")}

	bot, err := NewAutopilot(scanner, &fakeExecutor{paper: true}, config)
	if err != nil {
		t.Fatalf("new autopilot: %v", err)
	}
	report := bot.Run(context.Background())
	if report.HaltReason != "consecutive_failures" {
		t.Fatalf("expected consecutive_failures halt, got %q", report.HaltReason)
	}
	if report.State.ScansFailed != 2 {
		t.Fatalf("expected 2 failed scans, got %d", report.State.ScansFailed)
	}
}

func TestAutopilotHaltsWhenBudgetExhausted(t *testing.T) {
	bot, err := NewAutopilot(scanWith(), &fakeExecutor{paper: true}, testAutopilotConfig())
	if err != nil {
		t.Fatalf("new autopilot: %v", err)
	}
	bot.state.CashSpentUSD = 100
	report := bot.Run(context.Background())
	if report.HaltReason != "budget_exhausted" {
		t.Fatalf("expected budget_exhausted, got %q", report.HaltReason)
	}
	if report.State.Cycles != 0 {
		t.Fatalf("expected no cycles, got %d", report.State.Cycles)
	}
}

func TestAutopilotRespectsContextCancellation(t *testing.T) {
	bot, err := NewAutopilot(scanWith(), &fakeExecutor{paper: true}, testAutopilotConfig())
	if err != nil {
		t.Fatalf("new autopilot: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := bot.Run(ctx)
	if report.HaltReason != "context_done" {
		t.Fatalf("expected context_done, got %q", report.HaltReason)
	}
}
