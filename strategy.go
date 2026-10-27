package polymarket

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

type StrategyEngine struct {
	client       *Client
	analyzer     *MarketAnalyzer
	predictor    *AIPredictor
	riskManager  *RiskManager
	strategies   map[string]TradingStrategy
	mu           sync.RWMutex
	positions    map[string]*Position
	tradeHistory []Trade
	paperMode    bool
	executor     Executor
	decisions    *DecisionLogger
}

type TradingStrategy interface {
	Name() string
	Execute(ctx context.Context, signal *TradingSignal) (*TradeExecution, error)
	Validate(signal *TradingSignal) bool
	ProposedSize(signal *TradingSignal) float64
}

type TradingSignal struct {
	MarketID      string             `json:"market_id"`
	MarketTitle   string             `json:"market_title"`
	Strategy      string             `json:"strategy"`
	Direction     string             `json:"direction"`
	Strength      float64            `json:"strength"`
	Confidence    float64            `json:"confidence"`
	ExpectedValue float64            `json:"expected_value"`
	CurrentPrice  float64            `json:"current_price"`
	FairValue     float64            `json:"fair_value"`
	YesTokenID    string             `json:"yes_token_id,omitempty"`
	NoTokenID     string             `json:"no_token_id,omitempty"`
	Opportunity   *MarketOpportunity `json:"opportunity"`
	Prediction    *AIPrediction      `json:"prediction"`
	Assessment    *ValueAssessment   `json:"assessment"`
	ApprovedSize  float64            `json:"-"`
	Timestamp     time.Time          `json:"timestamp"`
}

type TradeExecution struct {
	MarketID  string    `json:"market_id"`
	Strategy  string    `json:"strategy"`
	Side      string    `json:"side"`
	Outcome   string    `json:"outcome"`
	Size      float64   `json:"size_usd"`
	Shares    float64   `json:"shares"`
	Price     float64   `json:"price"`
	FeeUSD    float64   `json:"fee_usd"`
	OrderID   string    `json:"order_id"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Reasoning string    `json:"reasoning"`
}

type Position struct {
	MarketID     string    `json:"market_id"`
	Outcome      string    `json:"outcome"`
	Size         float64   `json:"size_usd"`
	Shares       float64   `json:"shares"`
	AveragePrice float64   `json:"average_price"`
	CurrentValue float64   `json:"current_value"`
	PnL          float64   `json:"pnl"`
	PnLPercent   float64   `json:"pnl_percent"`
	OpenedAt     time.Time `json:"opened_at"`
	LastUpdated  time.Time `json:"last_updated"`
	Strategy     string    `json:"strategy"`
}

type ValueBetStrategy struct {
	engine *StrategyEngine
}

type ArbitrageStrategy struct {
	engine *StrategyEngine
}

type MomentumStrategy struct {
	engine *StrategyEngine
}

type HedgeStrategy struct {
	engine *StrategyEngine
}

func NewStrategyEngine(client *Client, analyzer *MarketAnalyzer, predictor *AIPredictor, riskManager *RiskManager) *StrategyEngine {
	engine := &StrategyEngine{
		client:       client,
		analyzer:     analyzer,
		predictor:    predictor,
		riskManager:  riskManager,
		strategies:   make(map[string]TradingStrategy),
		positions:    make(map[string]*Position),
		tradeHistory: make([]Trade, 0),
		paperMode:    true,
	}
	if client != nil {
		engine.executor = NewPaperExecutor(NewCLOBService(client), 0)
	}

	engine.RegisterStrategy(&ValueBetStrategy{engine: engine})
	engine.RegisterStrategy(&ArbitrageStrategy{engine: engine})
	engine.RegisterStrategy(&MomentumStrategy{engine: engine})
	engine.RegisterStrategy(&HedgeStrategy{engine: engine})

	return engine
}

func (se *StrategyEngine) RegisterStrategy(strategy TradingStrategy) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.strategies[strategy.Name()] = strategy
}

func (se *StrategyEngine) GetStrategy(name string) (TradingStrategy, bool) {
	se.mu.RLock()
	defer se.mu.RUnlock()
	strategy, ok := se.strategies[name]
	return strategy, ok
}

// SetPaperMode switches between book-walking paper fills and the configured
// live executor. Disabling paper mode without setting a live executor leaves
// the engine unable to execute.
func (se *StrategyEngine) SetPaperMode(enabled bool) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.paperMode = enabled
	if enabled {
		if se.client != nil {
			se.executor = NewPaperExecutor(NewCLOBService(se.client), 0)
		}
		return
	}
	se.executor = nil
}

func (se *StrategyEngine) SetExecutor(executor Executor) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.executor = executor
	se.paperMode = executor != nil && executor.IsPaper()
}

func (se *StrategyEngine) SetDecisionLogger(logger *DecisionLogger) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.decisions = logger
}

func (se *StrategyEngine) logDecision(record DecisionRecord) {
	se.mu.RLock()
	logger := se.decisions
	se.mu.RUnlock()
	if logger != nil {
		_ = logger.Write(record)
	}
}

func (se *StrategyEngine) reject(signal *TradingSignal, reason string, err error) (*TradeExecution, error) {
	se.logDecision(DecisionRecord{
		Type:     "skip",
		MarketID: signal.MarketID,
		Strategy: signal.Strategy,
		Reason:   reason + ": " + err.Error(),
		Signal:   signal,
	})
	return nil, err
}

func (se *StrategyEngine) ExecuteOrder(ctx context.Context, request OrderRequest) (*ExecutionFill, error) {
	se.mu.RLock()
	executor := se.executor
	se.mu.RUnlock()
	if executor == nil {
		return nil, fmt.Errorf("no executor configured")
	}
	return executor.Execute(ctx, request)
}

func (se *StrategyEngine) isPaperMode() bool {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.paperMode
}

func (se *StrategyEngine) ProcessSignal(ctx context.Context, signal *TradingSignal) (*TradeExecution, error) {
	if signal == nil {
		return nil, fmt.Errorf("signal is nil")
	}
	se.logDecision(DecisionRecord{Type: "signal", MarketID: signal.MarketID, Strategy: signal.Strategy, Signal: signal})
	if !se.riskManager.CanTrade() {
		return se.reject(signal, "risk_halted", fmt.Errorf("trading halted by risk manager"))
	}

	strategy, ok := se.GetStrategy(signal.Strategy)
	if !ok {
		return se.reject(signal, "unknown_strategy", fmt.Errorf("strategy %s not found", signal.Strategy))
	}

	if !strategy.Validate(signal) {
		return se.reject(signal, "validation_failed", fmt.Errorf("signal validation failed"))
	}

	requested := strategy.ProposedSize(signal)
	approved, err := se.riskManager.ApproveOrder(signal, requested)
	if err != nil {
		return se.reject(signal, "risk_rejected", fmt.Errorf("risk approval failed: %w", err))
	}
	signal.ApprovedSize = approved

	execution, err := strategy.Execute(ctx, signal)
	if err != nil {
		return se.reject(signal, "execution_failed", fmt.Errorf("strategy execution failed: %w", err))
	}
	if execution == nil {
		return se.reject(signal, "empty_execution", fmt.Errorf("strategy returned no execution"))
	}
	if execution.Size <= 0 || execution.Size > approved+1e-6 {
		return se.reject(signal, "size_exceeds_approval", fmt.Errorf("execution size %.2f USD exceeds approved size %.2f USD", execution.Size, approved))
	}

	switch strings.ToUpper(execution.Status) {
	case "FILLED", "MATCHED", "PARTIAL_FILLED", "PAPER_FILLED", "PAPER_PARTIAL_FILLED":
	default:
		return se.reject(signal, "unreconciled_status", fmt.Errorf("order status %q is not a fill; execution reconciliation is not implemented", execution.Status))
	}

	se.riskManager.RecordTrade(execution)
	se.updatePosition(execution)
	se.logDecision(DecisionRecord{Type: "fill", MarketID: signal.MarketID, Strategy: signal.Strategy, Signal: signal, Execution: execution})

	se.mu.Lock()
	se.tradeHistory = append(se.tradeHistory, Trade{
		ID:        execution.OrderID,
		MarketID:  execution.MarketID,
		Side:      execution.Side,
		Outcome:   execution.Outcome,
		Count:     execution.Shares,
		Price:     execution.Price,
		Timestamp: execution.Timestamp,
	})
	se.mu.Unlock()

	return execution, nil
}

func (s *ValueBetStrategy) Name() string {
	return "VALUE_BET"
}

func (s *ValueBetStrategy) Validate(signal *TradingSignal) bool {
	if signal == nil || signal.Assessment == nil {
		return false
	}
	if !validPrice(signal.CurrentPrice) {
		return false
	}
	if math.Abs(signal.Assessment.Edge) < 0.05 {
		return false
	}
	if signal.Confidence < 50 {
		return false
	}
	return true
}

func (s *ValueBetStrategy) ProposedSize(signal *TradingSignal) float64 {
	return s.engine.riskManager.CalculatePositionSize(signal)
}

func (s *ValueBetStrategy) Execute(ctx context.Context, signal *TradingSignal) (*TradeExecution, error) {
	outcome := "YES"
	price := signal.CurrentPrice
	if signal.Assessment.AIProbability < signal.Assessment.MarketImpliedProb {
		outcome = "NO"
		price = 1 - signal.CurrentPrice
	}
	if !validPrice(price) {
		return nil, fmt.Errorf("executable price %f is outside (0,1)", price)
	}

	sizeUSD := signal.ApprovedSize
	if sizeUSD <= 0 {
		return nil, fmt.Errorf("signal has no risk-approved size")
	}
	shares := sizeUSD / price

	fill, err := s.engine.ExecuteOrder(ctx, OrderRequest{
		MarketID:   signal.MarketID,
		MaxCashUSD: signal.ApprovedSize,
		TokenID:    tokenForOutcome(signal, outcome),
		Side:       "BUY",
		Outcome:    outcome,
		Shares:     shares,
		LimitPrice: price,
		OrderType:  "FAK",
	})
	if err != nil {
		return nil, err
	}

	return &TradeExecution{
		MarketID:  signal.MarketID,
		Strategy:  s.Name(),
		Side:      "BUY",
		Outcome:   outcome,
		Size:      math.Abs(fill.CashUSD),
		Shares:    fill.FilledShares,
		Price:     fill.AveragePrice,
		FeeUSD:    fill.FeeUSD,
		OrderID:   fill.OrderID,
		Status:    fill.Status,
		Timestamp: time.Now(),
		Reasoning: fmt.Sprintf("Edge: %.2f%%, Confidence: %.2f%%", signal.Assessment.EdgePercent, signal.Confidence),
	}, nil
}

func (s *ArbitrageStrategy) Name() string {
	return "ARBITRAGE"
}

func (s *ArbitrageStrategy) Validate(signal *TradingSignal) bool {
	if signal == nil || signal.Opportunity == nil {
		return false
	}
	if !validPrice(signal.CurrentPrice) {
		return false
	}
	if signal.Opportunity.RecommendedBet != "YES" && signal.Opportunity.RecommendedBet != "NO" {
		return false
	}
	if signal.Opportunity.ExpectedValue < 0.03 {
		return false
	}
	return true
}

func (s *ArbitrageStrategy) ProposedSize(signal *TradingSignal) float64 {
	expectedValue := signal.Opportunity.ExpectedValue
	confidence := signal.Opportunity.Confidence / 100.0
	size := 200.0 * expectedValue * 10 * confidence
	if size > 2000 {
		size = 2000
	}
	return size
}

func (s *ArbitrageStrategy) Execute(ctx context.Context, signal *TradingSignal) (*TradeExecution, error) {
	outcome := signal.Opportunity.RecommendedBet
	price := signal.CurrentPrice
	if outcome == "NO" {
		price = 1 - price
	}
	if !validPrice(price) {
		return nil, fmt.Errorf("executable price %f is outside (0,1)", price)
	}

	sizeUSD := signal.ApprovedSize
	if sizeUSD <= 0 {
		return nil, fmt.Errorf("signal has no risk-approved size")
	}
	shares := sizeUSD / price

	fill, err := s.engine.ExecuteOrder(ctx, OrderRequest{
		MarketID:   signal.MarketID,
		MaxCashUSD: signal.ApprovedSize,
		TokenID:    tokenForOutcome(signal, outcome),
		Side:       "BUY",
		Outcome:    outcome,
		Shares:     shares,
		LimitPrice: price,
		OrderType:  "FAK",
	})
	if err != nil {
		return nil, err
	}

	return &TradeExecution{
		MarketID:  signal.MarketID,
		Strategy:  s.Name(),
		Side:      "BUY",
		Outcome:   outcome,
		Size:      math.Abs(fill.CashUSD),
		Shares:    fill.FilledShares,
		Price:     fill.AveragePrice,
		FeeUSD:    fill.FeeUSD,
		OrderID:   fill.OrderID,
		Status:    fill.Status,
		Timestamp: time.Now(),
		Reasoning: signal.Opportunity.Reasoning,
	}, nil
}

func (s *MomentumStrategy) Name() string {
	return "MOMENTUM"
}

func (s *MomentumStrategy) Validate(signal *TradingSignal) bool {
	if signal == nil || !validPrice(signal.CurrentPrice) {
		return false
	}
	if signal.Strength < 0.02 {
		return false
	}
	if signal.Confidence < 60 {
		return false
	}
	return true
}

func (s *MomentumStrategy) ProposedSize(signal *TradingSignal) float64 {
	strength := math.Abs(signal.Strength)
	confidence := signal.Confidence / 100.0
	size := 150.0 * strength * 100 * confidence
	if size > 1500 {
		size = 1500
	}
	return size
}

func (s *MomentumStrategy) Execute(ctx context.Context, signal *TradingSignal) (*TradeExecution, error) {
	outcome := "YES"
	price := signal.CurrentPrice
	if signal.Direction == "DOWN" {
		outcome = "NO"
		price = 1 - signal.CurrentPrice
	}
	// Use a limit price with a small adverse buffer instead of an unbounded
	// market order; a market order can cross the entire thin book.
	price *= 1.01
	if price >= 0.99 {
		price = 0.99
	}
	if !validPrice(price) {
		return nil, fmt.Errorf("executable price %f is outside (0,1)", price)
	}

	sizeUSD := signal.ApprovedSize
	if sizeUSD <= 0 {
		return nil, fmt.Errorf("signal has no risk-approved size")
	}
	shares := sizeUSD / price

	fill, err := s.engine.ExecuteOrder(ctx, OrderRequest{
		MarketID:   signal.MarketID,
		MaxCashUSD: signal.ApprovedSize,
		TokenID:    tokenForOutcome(signal, outcome),
		Side:       "BUY",
		Outcome:    outcome,
		Shares:     shares,
		LimitPrice: price,
		OrderType:  "FAK",
	})
	if err != nil {
		return nil, err
	}

	return &TradeExecution{
		MarketID:  signal.MarketID,
		Strategy:  s.Name(),
		Side:      "BUY",
		Outcome:   outcome,
		Size:      math.Abs(fill.CashUSD),
		Shares:    fill.FilledShares,
		Price:     fill.AveragePrice,
		FeeUSD:    fill.FeeUSD,
		OrderID:   fill.OrderID,
		Status:    fill.Status,
		Timestamp: time.Now(),
		Reasoning: fmt.Sprintf("Momentum signal: %.2f%% strength", signal.Strength*100),
	}, nil
}

func (s *HedgeStrategy) Name() string {
	return "HEDGE"
}

func (s *HedgeStrategy) positionFor(signal *TradingSignal) *Position {
	if signal.Direction == "YES" || signal.Direction == "NO" {
		if position := s.engine.GetPositionByOutcome(signal.MarketID, signal.Direction); position != nil {
			return position
		}
	}
	return s.engine.GetPosition(signal.MarketID)
}

func (s *HedgeStrategy) Validate(signal *TradingSignal) bool {
	if signal == nil || !validPrice(signal.CurrentPrice) {
		return false
	}
	position := s.positionFor(signal)
	if position == nil {
		return false
	}
	return position.PnL > position.Size*0.5
}

func (s *HedgeStrategy) ProposedSize(signal *TradingSignal) float64 {
	position := s.positionFor(signal)
	if position == nil {
		return 0
	}
	return position.Size * 0.5
}

func (s *HedgeStrategy) Execute(ctx context.Context, signal *TradingSignal) (*TradeExecution, error) {
	position := s.positionFor(signal)
	if position == nil {
		return nil, fmt.Errorf("no position to hedge")
	}

	hedgeOutcome := "YES"
	price := signal.CurrentPrice
	if position.Outcome == "YES" {
		hedgeOutcome = "NO"
		price = 1 - signal.CurrentPrice
	}
	if !validPrice(price) {
		return nil, fmt.Errorf("executable price %f is outside (0,1)", price)
	}

	sizeUSD := signal.ApprovedSize
	if sizeUSD <= 0 {
		return nil, fmt.Errorf("signal has no risk-approved size")
	}
	shares := sizeUSD / price

	fill, err := s.engine.ExecuteOrder(ctx, OrderRequest{
		MarketID:   signal.MarketID,
		MaxCashUSD: signal.ApprovedSize,
		TokenID:    tokenForOutcome(signal, hedgeOutcome),
		Side:       "BUY",
		Outcome:    hedgeOutcome,
		Shares:     shares,
		LimitPrice: price,
		OrderType:  "FAK",
	})
	if err != nil {
		return nil, err
	}

	return &TradeExecution{
		MarketID:  signal.MarketID,
		Strategy:  s.Name(),
		Side:      "BUY",
		Outcome:   hedgeOutcome,
		Size:      math.Abs(fill.CashUSD),
		Shares:    fill.FilledShares,
		Price:     fill.AveragePrice,
		FeeUSD:    fill.FeeUSD,
		OrderID:   fill.OrderID,
		Status:    fill.Status,
		Timestamp: time.Now(),
		Reasoning: fmt.Sprintf("Hedge existing %s position", position.Outcome),
	}, nil
}

func (se *StrategyEngine) updatePosition(execution *TradeExecution) {
	se.mu.Lock()
	defer se.mu.Unlock()

	key := execution.MarketID + "_" + execution.Outcome
	shares := execution.Shares
	if shares <= 0 && execution.Price > 0 {
		shares = execution.Size / execution.Price
	}

	if existing, ok := se.positions[key]; ok {
		totalSizeUSD := existing.Size + execution.Size
		totalShares := existing.Shares + shares
		if totalShares > 0 {
			existing.AveragePrice = totalSizeUSD / totalShares
		}
		existing.Size = totalSizeUSD
		existing.Shares = totalShares
		existing.CurrentValue = totalShares * execution.Price
		existing.PnL = existing.CurrentValue - existing.Size
		if existing.Size > 0 {
			existing.PnLPercent = existing.PnL / existing.Size
		}
		existing.LastUpdated = time.Now()
	} else {
		se.positions[key] = &Position{
			MarketID:     execution.MarketID,
			Outcome:      execution.Outcome,
			Size:         execution.Size,
			Shares:       shares,
			AveragePrice: execution.Price,
			CurrentValue: shares * execution.Price,
			PnL:          0,
			PnLPercent:   0,
			OpenedAt:     time.Now(),
			LastUpdated:  time.Now(),
			Strategy:     execution.Strategy,
		}
	}
}

// UpdatePositionPrice marks a position to market. Call this before evaluating
// HEDGE signals, otherwise PnL stays at zero and hedging never triggers.
func (se *StrategyEngine) UpdatePositionPrice(marketID, outcome string, currentPrice float64) error {
	if !validPrice(currentPrice) {
		return fmt.Errorf("price %f is outside (0,1)", currentPrice)
	}
	se.mu.Lock()
	defer se.mu.Unlock()

	key := marketID + "_" + outcome
	position, ok := se.positions[key]
	if !ok {
		return fmt.Errorf("position %s/%s not found", marketID, outcome)
	}
	position.CurrentValue = position.Shares * currentPrice
	position.PnL = position.CurrentValue - position.Size
	if position.Size > 0 {
		position.PnLPercent = position.PnL / position.Size
	}
	position.LastUpdated = time.Now()
	return nil
}

func (se *StrategyEngine) GetPosition(marketID string) *Position {
	se.mu.RLock()
	defer se.mu.RUnlock()

	var best *Position
	for _, pos := range se.positions {
		if pos.MarketID != marketID {
			continue
		}
		if best == nil || pos.Size > best.Size {
			copy := *pos
			best = &copy
		}
	}
	return best
}

func (se *StrategyEngine) GetPositionByOutcome(marketID, outcome string) *Position {
	se.mu.RLock()
	defer se.mu.RUnlock()

	if pos, ok := se.positions[marketID+"_"+outcome]; ok {
		copy := *pos
		return &copy
	}
	return nil
}

func (se *StrategyEngine) GetAllPositions() map[string]*Position {
	se.mu.RLock()
	defer se.mu.RUnlock()

	positions := make(map[string]*Position, len(se.positions))
	for k, v := range se.positions {
		copy := *v
		positions[k] = &copy
	}
	return positions
}

func (se *StrategyEngine) ClosePosition(ctx context.Context, marketID, outcome string) error {
	se.mu.Lock()
	key := marketID + "_" + outcome
	position, ok := se.positions[key]
	if !ok {
		se.mu.Unlock()
		return fmt.Errorf("position not found")
	}
	if !se.paperMode {
		se.mu.Unlock()
		return fmt.Errorf("closing a live position requires a signed SELL order; execution reconciliation is not implemented")
	}
	settlement := *position
	delete(se.positions, key)
	se.mu.Unlock()

	// Paper mode has no exchange orders. Release the cost basis and apply the
	// last marked-to-market PnL so risk capital is reusable.
	se.riskManager.RecordSettlement(marketID, settlement.Size, settlement.PnL)
	return nil
}

func (se *StrategyEngine) GetTradeHistory() []Trade {
	se.mu.RLock()
	defer se.mu.RUnlock()

	history := make([]Trade, len(se.tradeHistory))
	copy(history, se.tradeHistory)
	return history
}

func (se *StrategyEngine) CalculateTotalPnL() float64 {
	se.mu.RLock()
	defer se.mu.RUnlock()

	totalPnL := 0.0
	for _, position := range se.positions {
		totalPnL += position.PnL
	}
	return totalPnL
}

func validPrice(price float64) bool {
	return !math.IsNaN(price) && !math.IsInf(price, 0) && price > 0 && price < 1
}

func tokenForOutcome(signal *TradingSignal, outcome string) string {
	if outcome == "NO" {
		return signal.NoTokenID
	}
	return signal.YesTokenID
}
