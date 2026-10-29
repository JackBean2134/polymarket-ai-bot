package polymarket

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// BasketScanner is the read-only dependency the autopilot needs. The
// ArbitrageScanner implements it; tests can supply a deterministic fake.
type BasketScanner interface {
	ScanEvents(ctx context.Context, limit int, minEdgeUSD float64) (*ArbitrageScanResult, error)
}

// AutopilotConfig describes a deliberately narrow autonomous loop. It only
// trades structural YES/NO and negRisk baskets whose net edge is positive at
// the moment of execution, and it never sends live orders.
type AutopilotConfig struct {
	// ScanEvents is the number of top events fetched per cycle.
	ScanEvents int `json:"scan_events"`
	// Interval is the pause between cycles.
	Interval time.Duration `json:"interval"`
	// CycleTimeout bounds one scan.
	CycleTimeout time.Duration `json:"cycle_timeout"`
	// MinNetEdgeUSD is the minimum net edge per $1 basket payout.
	MinNetEdgeUSD float64 `json:"min_net_edge_usd"`
	// MaxPerBasketUSD caps the cash used by a single basket.
	MaxPerBasketUSD float64 `json:"max_per_basket_usd"`
	// MaxTotalUSD is the whole-run cash budget.
	MaxTotalUSD float64 `json:"max_total_usd"`
	// MaxPerEventUSD caps cumulative spend inside one event.
	MaxPerEventUSD float64 `json:"max_per_event_usd"`
	// MaxUnhedgedUSD halts the run once unhedged (partial) leg notional
	// reaches this amount. It is cumulative for the run, not per calendar day.
	MaxUnhedgedUSD float64 `json:"max_unhedged_usd"`
	// MaxCycles is the number of cycles to run (0 = until the context ends).
	MaxCycles int `json:"max_cycles"`
	// MaxConsecutiveFailures halts the run after repeated execution failures.
	MaxConsecutiveFailures int `json:"max_consecutive_failures"`
	// EventCooldown prevents re-buying the same event immediately.
	EventCooldown time.Duration `json:"event_cooldown"`
	// StopFile is the kill switch shared with the rest of the tooling.
	StopFile string `json:"stop_file,omitempty"`
	// HeartbeatFile is touched after every successful cycle.
	HeartbeatFile string `json:"heartbeat_file,omitempty"`
	// DecisionLog appends JSONL audit records when set.
	DecisionLog string `json:"decision_log,omitempty"`
}

// DefaultAutopilotConfig returns conservative defaults: a $100 budget, $25 per
// basket and a 2 cent per $1 basket edge floor.
func DefaultAutopilotConfig() AutopilotConfig {
	return AutopilotConfig{
		ScanEvents:             20,
		Interval:               120 * time.Second,
		CycleTimeout:           120 * time.Second,
		MinNetEdgeUSD:          0.02,
		MaxPerBasketUSD:        25,
		MaxTotalUSD:            100,
		MaxPerEventUSD:         25,
		MaxUnhedgedUSD:         25,
		MaxCycles:              0,
		MaxConsecutiveFailures: 10,
		EventCooldown:          5 * time.Minute,
	}
}

// normalizeAutopilotConfig applies defaults and rejects unsafe values.
func normalizeAutopilotConfig(config AutopilotConfig) (AutopilotConfig, error) {
	defaults := DefaultAutopilotConfig()
	if config.ScanEvents <= 0 {
		config.ScanEvents = defaults.ScanEvents
	}
	if config.ScanEvents > 200 {
		config.ScanEvents = 200
	}
	if config.Interval <= 0 {
		config.Interval = defaults.Interval
	}
	if config.CycleTimeout <= 0 {
		config.CycleTimeout = defaults.CycleTimeout
	}
	if config.MinNetEdgeUSD <= 0 {
		config.MinNetEdgeUSD = defaults.MinNetEdgeUSD
	}
	if config.MaxPerBasketUSD <= 0 {
		config.MaxPerBasketUSD = defaults.MaxPerBasketUSD
	}
	if config.MaxTotalUSD <= 0 {
		config.MaxTotalUSD = defaults.MaxTotalUSD
	}
	if config.MaxPerEventUSD <= 0 {
		config.MaxPerEventUSD = defaults.MaxPerEventUSD
	}
	if config.MaxUnhedgedUSD <= 0 {
		config.MaxUnhedgedUSD = defaults.MaxUnhedgedUSD
	}
	if config.MaxConsecutiveFailures <= 0 {
		config.MaxConsecutiveFailures = defaults.MaxConsecutiveFailures
	}
	if config.EventCooldown < 0 {
		config.EventCooldown = defaults.EventCooldown
	}
	if config.MaxCycles < 0 {
		return config, fmt.Errorf("max cycles must be zero (unlimited) or positive")
	}
	if config.MinNetEdgeUSD < 0.001 {
		return config, fmt.Errorf("min net edge %.4f is below 0.001; refusing to trade dust edges", config.MinNetEdgeUSD)
	}
	if config.MaxPerBasketUSD > config.MaxTotalUSD {
		return config, fmt.Errorf("max per basket $%.2f exceeds total budget $%.2f", config.MaxPerBasketUSD, config.MaxTotalUSD)
	}
	if config.MaxPerEventUSD > config.MaxTotalUSD {
		config.MaxPerEventUSD = config.MaxTotalUSD
	}
	if config.MaxUnhedgedUSD > config.MaxTotalUSD {
		config.MaxUnhedgedUSD = config.MaxTotalUSD
	}
	return config, nil
}

// AutopilotState is the running ledger. LockedProfitUSD is edge that is
// mathematically locked in a completed basket but only becomes cash at market
// settlement; it is not realised profit.
type AutopilotState struct {
	Cycles              int       `json:"cycles"`
	ScansFailed         int       `json:"scans_failed"`
	OpportunitiesSeen   int       `json:"opportunities_seen"`
	BasketsFilled       int       `json:"baskets_filled"`
	BasketsAborted      int       `json:"baskets_aborted"`
	SharesFilled        float64   `json:"shares_filled"`
	CashSpentUSD        float64   `json:"cash_spent_usd"`
	FeesPaidUSD         float64   `json:"fees_paid_usd"`
	LockedProfitUSD     float64   `json:"locked_profit_usd"`
	AssumedLossUSD      float64   `json:"assumed_loss_usd"`
	NakedLegsUSD        float64   `json:"naked_legs_usd"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	Halted              bool      `json:"halted"`
	HaltReason          string    `json:"halt_reason,omitempty"`
	LastCycleAt         time.Time `json:"last_cycle_at,omitempty"`
}

type AutopilotFill struct {
	MarketID    string  `json:"market_id"`
	Outcome     string  `json:"outcome"`
	TokenID     string  `json:"token_id"`
	Shares      float64 `json:"shares"`
	VWAP        float64 `json:"vwap"`
	NotionalUSD float64 `json:"notional_usd"`
	FeeUSD      float64 `json:"fee_usd"`
}

type AutopilotBasket struct {
	EventID         string          `json:"event_id"`
	EventSlug       string          `json:"event_slug,omitempty"`
	Category        string          `json:"category,omitempty"`
	Kind            string          `json:"kind"`
	Title           string          `json:"title"`
	Shares          float64         `json:"shares"`
	PayoutUSD       float64         `json:"payout_usd"`
	CostUSD         float64         `json:"cost_usd"`
	FeeUSD          float64         `json:"fee_usd"`
	LockedProfitUSD float64         `json:"locked_profit_usd"`
	Legs            []AutopilotFill `json:"legs"`
	FilledAt        time.Time       `json:"filled_at"`
}

type AutopilotAbort struct {
	EventID          string    `json:"event_id"`
	Kind             string    `json:"kind"`
	Reason           string    `json:"reason"`
	Baskets          float64   `json:"baskets"`
	NakedNotionalUSD float64   `json:"naked_notional_usd"`
	Timestamp        time.Time `json:"timestamp"`
}

// AutopilotCandidate is the compact record of one scanned opportunity. It is
// logged even when the basket is never traded, so opportunity frequency and
// edge distribution can be measured offline.
type AutopilotCandidate struct {
	EventID      string  `json:"event_id"`
	EventSlug    string  `json:"event_slug,omitempty"`
	Category     string  `json:"category,omitempty"`
	Kind         string  `json:"kind"`
	CostUSD      float64 `json:"cost_usd"`
	NetEdgeUSD   float64 `json:"net_edge_usd"`
	MaxBaskets   float64 `json:"max_baskets"`
	NetProfitUSD float64 `json:"net_profit_usd"`
}

type AutopilotCycle struct {
	Index           int                  `json:"index"`
	StartedAt       time.Time            `json:"started_at"`
	FinishedAt      time.Time            `json:"finished_at"`
	EventsScanned   int                  `json:"events_scanned"`
	Opportunities   int                  `json:"opportunities"`
	Candidates      []AutopilotCandidate `json:"candidates,omitempty"`
	Filled          []AutopilotBasket    `json:"filled,omitempty"`
	Aborted         []AutopilotAbort     `json:"aborted,omitempty"`
	DeployedUSD     float64              `json:"deployed_usd"`
	LockedProfitUSD float64              `json:"locked_profit_usd"`
	Error           string               `json:"error,omitempty"`
}

type AutopilotReport struct {
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	Paper      bool             `json:"paper"`
	Config     AutopilotConfig  `json:"config"`
	State      AutopilotState   `json:"state"`
	Cycles     []AutopilotCycle `json:"cycles"`
	HaltReason string           `json:"halt_reason"`
	Warning    string           `json:"warning"`
}

// Autopilot runs a supervised, paper-only arbitrage loop. It refuses any
// executor that is not a paper executor: handing a live wallet to an
// unattended loop is exactly the failure mode this project warns about.
type Autopilot struct {
	scanner  BasketScanner
	executor Executor
	config   AutopilotConfig
	logger   *DecisionLogger

	state         AutopilotState
	eventExposure map[string]float64
	lastBasketAt  map[string]time.Time
}

func NewAutopilot(scanner BasketScanner, executor Executor, config AutopilotConfig) (*Autopilot, error) {
	if scanner == nil {
		return nil, fmt.Errorf("autopilot requires a basket scanner")
	}
	if executor == nil {
		return nil, fmt.Errorf("autopilot requires an executor")
	}
	if !executor.IsPaper() {
		return nil, fmt.Errorf("autopilot only supports paper execution; live trading must stay manual and gated")
	}
	normalized, err := normalizeAutopilotConfig(config)
	if err != nil {
		return nil, err
	}

	bot := &Autopilot{
		scanner:       scanner,
		executor:      executor,
		config:        normalized,
		eventExposure: make(map[string]float64),
		lastBasketAt:  make(map[string]time.Time),
	}
	if normalized.DecisionLog != "" {
		logger, err := NewDecisionLogger(normalized.DecisionLog)
		if err != nil {
			return nil, err
		}
		bot.logger = logger
	}
	return bot, nil
}

// Close releases the decision log file.
func (a *Autopilot) Close() error {
	if a.logger == nil {
		return nil
	}
	return a.logger.Close()
}

// State returns a copy of the running ledger.
func (a *Autopilot) State() AutopilotState {
	return a.state
}

// Run executes cycles until a halt condition, the cycle limit or ctx ends.
func (a *Autopilot) Run(ctx context.Context) *AutopilotReport {
	report := &AutopilotReport{
		StartedAt: time.Now().UTC(),
		Paper:     true,
		Config:    a.config,
		Cycles:    make([]AutopilotCycle, 0),
		Warning:   "paper mode: no orders are sent; locked_profit_usd only becomes cash at settlement",
	}

	for {
		if reason := a.stopReason(ctx); reason != "" {
			report.HaltReason = reason
			break
		}

		cycle := a.runCycle(ctx)
		report.Cycles = append(report.Cycles, *cycle)

		if a.state.Halted {
			report.HaltReason = a.state.HaltReason
			break
		}
		if a.config.MaxCycles > 0 && a.state.Cycles >= a.config.MaxCycles {
			report.HaltReason = "max_cycles"
			break
		}
		if !a.wait(ctx) {
			report.HaltReason = "context_done"
			break
		}
	}

	report.FinishedAt = time.Now().UTC()
	report.State = a.state
	if report.HaltReason == "" {
		report.HaltReason = "stopped"
	}
	return report
}

// stopReason checks every hard brake before a new cycle starts.
func (a *Autopilot) stopReason(ctx context.Context) string {
	if a.state.Halted {
		return a.state.HaltReason
	}
	if err := CheckStopFile(a.config.StopFile); err != nil {
		a.halt("stop_file")
		return "stop_file"
	}
	if ctx.Err() != nil {
		return "context_done"
	}
	if a.state.CashSpentUSD >= a.config.MaxTotalUSD {
		return "budget_exhausted"
	}
	return ""
}

func (a *Autopilot) wait(ctx context.Context) bool {
	if a.config.Interval <= 0 {
		return true
	}
	timer := time.NewTimer(a.config.Interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (a *Autopilot) runCycle(ctx context.Context) *AutopilotCycle {
	cycle := &AutopilotCycle{Index: a.state.Cycles + 1, StartedAt: time.Now().UTC()}
	a.state.Cycles = cycle.Index

	scanCtx, cancel := context.WithTimeout(ctx, a.config.CycleTimeout)
	result, err := a.scanner.ScanEvents(scanCtx, a.config.ScanEvents, a.config.MinNetEdgeUSD)
	cancel()
	if err != nil {
		a.state.ScansFailed++
		a.state.ConsecutiveFailures++
		cycle.Error = err.Error()
		cycle.FinishedAt = time.Now().UTC()
		a.state.LastCycleAt = cycle.FinishedAt
		_ = a.log(DecisionRecord{Type: "autopilot_scan_error", Reason: err.Error()})
		a.maybeHalt()
		return cycle
	}

	cycle.EventsScanned = result.Stats.EventsScanned
	cycle.Opportunities = len(result.Opportunities)
	a.state.OpportunitiesSeen += len(result.Opportunities)

	opportunities := append([]ArbitrageOpportunity(nil), result.Opportunities...)
	sort.SliceStable(opportunities, func(i, j int) bool {
		return opportunities[i].NetProfitUSD > opportunities[j].NetProfitUSD
	})
	if len(opportunities) > 0 {
		cycle.Candidates = make([]AutopilotCandidate, 0, len(opportunities))
		for _, opp := range opportunities {
			cycle.Candidates = append(cycle.Candidates, AutopilotCandidate{
				EventID:      opp.EventID,
				EventSlug:    opp.EventSlug,
				Category:     opp.Category,
				Kind:         opp.Kind,
				CostUSD:      opp.CostUSD,
				NetEdgeUSD:   opp.NetEdgeUSD,
				MaxBaskets:   opp.MaxBaskets,
				NetProfitUSD: opp.NetProfitUSD,
			})
		}
	}

	for _, opp := range opportunities {
		if ctx.Err() != nil {
			break
		}
		if a.state.CashSpentUSD >= a.config.MaxTotalUSD {
			break
		}
		if a.inCooldown(opp.EventID) {
			continue
		}

		basket, abort := a.executeBasket(ctx, opp)
		switch {
		case basket != nil:
			spent := basket.CostUSD + basket.FeeUSD
			a.state.BasketsFilled++
			a.state.SharesFilled += basket.Shares
			a.state.CashSpentUSD += spent
			a.state.FeesPaidUSD += basket.FeeUSD
			a.state.LockedProfitUSD += basket.LockedProfitUSD
			a.state.ConsecutiveFailures = 0
			a.eventExposure[opp.EventID] += spent
			a.lastBasketAt[opp.EventID] = basket.FilledAt
			cycle.Filled = append(cycle.Filled, *basket)
			cycle.DeployedUSD += spent
			cycle.LockedProfitUSD += basket.LockedProfitUSD
			_ = a.log(DecisionRecord{
				Type:      "autopilot_basket",
				MarketID:  opp.EventID,
				Strategy:  "ARBITRAGE",
				Reason:    basket.Kind,
				Execution: nil,
				State:     basket,
			})
		case abort != nil:
			a.state.BasketsAborted++
			a.state.NakedLegsUSD += abort.NakedNotionalUSD
			a.state.AssumedLossUSD += abort.NakedNotionalUSD
			a.state.CashSpentUSD += abort.NakedNotionalUSD
			a.state.ConsecutiveFailures++
			cycle.Aborted = append(cycle.Aborted, *abort)
			_ = a.log(DecisionRecord{
				Type:     "autopilot_abort",
				MarketID: abort.EventID,
				Strategy: "ARBITRAGE",
				Reason:   abort.Reason,
				State:    abort,
			})
		}
	}

	_ = WriteHeartbeat(a.config.HeartbeatFile)
	cycle.FinishedAt = time.Now().UTC()
	a.state.LastCycleAt = cycle.FinishedAt
	a.maybeHalt()
	// A per-cycle record makes the JSONL useful for measuring opportunity
	// frequency even when nothing was traded.
	_ = a.log(DecisionRecord{Type: "autopilot_cycle", State: cycle})
	return cycle
}

func (a *Autopilot) inCooldown(eventID string) bool {
	if a.config.EventCooldown <= 0 || eventID == "" {
		return false
	}
	last, ok := a.lastBasketAt[eventID]
	if !ok {
		return false
	}
	return time.Since(last) < a.config.EventCooldown
}

func (a *Autopilot) maybeHalt() {
	switch {
	case a.state.AssumedLossUSD >= a.config.MaxUnhedgedUSD:
		a.halt("unhedged_exposure_limit")
	case a.state.ConsecutiveFailures >= a.config.MaxConsecutiveFailures:
		a.halt("consecutive_failures")
	}
}

func (a *Autopilot) halt(reason string) {
	if a.state.Halted {
		return
	}
	a.state.Halted = true
	a.state.HaltReason = reason
	_ = a.log(DecisionRecord{Type: "autopilot_halt", Reason: reason})
}

func (a *Autopilot) log(record DecisionRecord) error {
	if a.logger == nil {
		return nil
	}
	return a.logger.Write(record)
}

func supportedBasketKind(kind string) bool {
	switch kind {
	case "BINARY_YES_NO", "NEGRISK_YES", "NEGRISK_NO":
		return true
	default:
		return false
	}
}

func (a *Autopilot) abort(opp ArbitrageOpportunity, reason string, shares, nakedNotional float64) *AutopilotAbort {
	return &AutopilotAbort{
		EventID:          opp.EventID,
		Kind:             opp.Kind,
		Reason:           reason,
		Baskets:          shares,
		NakedNotionalUSD: nakedNotional,
		Timestamp:        time.Now().UTC(),
	}
}

// executeBasket buys every leg of one basket at its quoted ask. A basket is
// only recorded when all legs fill completely and the edge survives the actual
// VWAP. Any partial basket is treated as a naked position and, conservatively,
// counted as a total loss for the unhedged-exposure brake.
func (a *Autopilot) executeBasket(ctx context.Context, opp ArbitrageOpportunity) (*AutopilotBasket, *AutopilotAbort) {
	if opp.PayoutUSD <= 0 || opp.CostUSD <= 0 || len(opp.Legs) < 2 {
		return nil, nil
	}
	if !supportedBasketKind(opp.Kind) {
		return nil, a.abort(opp, "unsupported_kind", 0, 0)
	}

	remainingBudget := a.config.MaxTotalUSD - a.state.CashSpentUSD
	eventRemaining := a.config.MaxPerEventUSD - a.eventExposure[opp.EventID]
	if remainingBudget <= 0 || eventRemaining <= 0 {
		return nil, nil
	}

	maxBaskets := opp.MaxBaskets
	if maxBaskets <= 0 {
		// Hand-built opportunities may omit depth; fall back to the budget.
		maxBaskets = remainingBudget / opp.CostUSD
	}
	maxShares := math.Min(maxBaskets, remainingBudget/opp.CostUSD)
	maxShares = math.Min(maxShares, eventRemaining/opp.CostUSD)
	maxShares = math.Min(maxShares, a.config.MaxPerBasketUSD/opp.CostUSD)
	shares := math.Floor(maxShares + 1e-9)
	if shares < 1 {
		return nil, nil
	}
	if shares > maxBaskets {
		shares = math.Floor(maxBaskets)
	}

	fills := make([]AutopilotFill, 0, len(opp.Legs))
	totalNotional := 0.0
	totalFee := 0.0
	naked := 0.0

	for _, leg := range opp.Legs {
		if leg.Ask <= 0 || leg.Ask >= 1 || strings.TrimSpace(leg.TokenID) == "" {
			return nil, a.abort(opp, "invalid_leg", shares, naked)
		}
		fill, err := a.executor.Execute(ctx, OrderRequest{
			MarketID:   leg.MarketID,
			TokenID:    leg.TokenID,
			Side:       "BUY",
			Outcome:    leg.Outcome,
			Shares:     shares,
			LimitPrice: leg.Ask,
			OrderType:  "FAK",
		})
		if err != nil {
			return nil, a.abort(opp, "leg_error: "+err.Error(), shares, naked)
		}
		if fill == nil || fill.FilledShares <= 0 || fill.NotionalUSD <= 0 {
			return nil, a.abort(opp, "leg_no_fill", shares, naked)
		}

		fee := takerFeeForLeg(leg, fill.AveragePrice, fill.FilledShares)
		if fee < 0 {
			fee = 0
		}
		totalNotional += fill.NotionalUSD
		totalFee += fee
		naked += fill.NotionalUSD + fee
		fills = append(fills, AutopilotFill{
			MarketID:    leg.MarketID,
			Outcome:     leg.Outcome,
			TokenID:     leg.TokenID,
			Shares:      fill.FilledShares,
			VWAP:        fill.AveragePrice,
			NotionalUSD: fill.NotionalUSD,
			FeeUSD:      fee,
		})

		if fill.FilledShares < shares-1e-9 {
			return nil, a.abort(opp, "leg_partial_fill", shares, naked)
		}
	}

	if len(fills) != len(opp.Legs) {
		return nil, a.abort(opp, "incomplete_basket", shares, naked)
	}

	locked := opp.PayoutUSD*shares - totalNotional - totalFee
	if locked < a.config.MinNetEdgeUSD*shares-1e-9 {
		return nil, a.abort(opp, "edge_below_minimum", shares, naked)
	}

	return &AutopilotBasket{
		EventID:         opp.EventID,
		EventSlug:       opp.EventSlug,
		Category:        opp.Category,
		Kind:            opp.Kind,
		Title:           opp.Title,
		Shares:          shares,
		PayoutUSD:       opp.PayoutUSD * shares,
		CostUSD:         totalNotional,
		FeeUSD:          totalFee,
		LockedProfitUSD: locked,
		Legs:            fills,
		FilledAt:        time.Now().UTC(),
	}, nil
}

// String renders a compact one-line summary for CLI output.
func (s AutopilotState) String() string {
	return fmt.Sprintf("cycles=%d filled=%d aborted=%d spent=$%.2f locked=$%.2f assumed_loss=$%.2f halted=%v",
		s.Cycles, s.BasketsFilled, s.BasketsAborted, s.CashSpentUSD, s.LockedProfitUSD, s.AssumedLossUSD, s.Halted)
}
