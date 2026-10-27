package polymarket

import (
	"fmt"
	"math"
	"sync"
	"time"
)

type RiskManager struct {
	config          *RiskConfig
	mu              sync.RWMutex
	totalCapital    float64
	usedCapital     float64
	totalPnL        float64
	dailyPnL        float64
	dailyLossLimit  float64
	maxDrawdown     float64
	peakCapital     float64
	currentDrawdown float64
	tradingHalted   bool
	haltReason      string
	tradeCount      int
	dailyTradeCount int
	lastResetDate   time.Time
	tradeHistory    []TradeRecord
	positionLimits  map[string]float64
}

type RiskConfig struct {
	TotalCapital         float64 `json:"total_capital"`
	MaxPositionSize      float64 `json:"max_position_size"`
	MaxPositionPercent   float64 `json:"max_position_percent"`
	DailyLossLimit       float64 `json:"daily_loss_limit"`
	MaxDrawdown          float64 `json:"max_drawdown"`
	MaxTradeCount        int     `json:"max_trade_count"`
	DailyTradeLimit      int     `json:"daily_trade_limit"`
	MaxOpenPositions     int     `json:"max_open_positions"`
	KellyMultiplier      float64 `json:"kelly_multiplier"`
	MinConfidence        float64 `json:"min_confidence"`
	MaxLiquiditySlippage float64 `json:"max_liquidity_slippage"`
	DiversificationLimit float64 `json:"diversification_limit"`
}

type TradeRecord struct {
	MarketID  string    `json:"market_id"`
	Strategy  string    `json:"strategy"`
	Side      string    `json:"side"`
	Size      float64   `json:"size"`
	Price     float64   `json:"price"`
	PnL       float64   `json:"pnl"`
	Timestamp time.Time `json:"timestamp"`
}

type PositionLimits struct {
	MaxPerMarket          float64 `json:"max_per_market"`
	MaxPerOutcome         float64 `json:"max_per_outcome"`
	MaxTotalExposure      float64 `json:"max_total_exposure"`
	MaxCorrelatedExposure float64 `json:"max_correlated_exposure"`
}

func NewRiskManager(config *RiskConfig) *RiskManager {
	defaults := DefaultConfig().Risk
	if config == nil {
		config = &RiskConfig{}
	}

	normalized := *config
	if normalized.TotalCapital <= 0 {
		normalized.TotalCapital = defaults.TotalCapital
	}
	if normalized.MaxPositionSize <= 0 {
		normalized.MaxPositionSize = defaults.MaxPositionSize
	}
	if normalized.MaxPositionPercent <= 0 {
		normalized.MaxPositionPercent = defaults.MaxPositionPercent
	}
	if normalized.DailyLossLimit <= 0 {
		normalized.DailyLossLimit = defaults.DailyLossLimit
	}
	if normalized.MaxDrawdown <= 0 {
		normalized.MaxDrawdown = defaults.MaxDrawdown
	}
	if normalized.DailyTradeLimit <= 0 {
		normalized.DailyTradeLimit = defaults.DailyTradeLimit
	}
	if normalized.MaxTradeCount <= 0 {
		normalized.MaxTradeCount = defaults.MaxTradeCount
	}
	if normalized.MaxOpenPositions <= 0 {
		normalized.MaxOpenPositions = defaults.MaxOpenPositions
	}
	if normalized.KellyMultiplier <= 0 {
		normalized.KellyMultiplier = defaults.KellyMultiplier
	}
	if normalized.MinConfidence <= 0 {
		normalized.MinConfidence = defaults.MinConfidence
	}
	if normalized.MaxLiquiditySlippage <= 0 {
		normalized.MaxLiquiditySlippage = defaults.MaxLiquiditySlippage
	}
	if normalized.DiversificationLimit <= 0 {
		normalized.DiversificationLimit = defaults.DiversificationLimit
	}
	config = &normalized

	rm := &RiskManager{
		config:         config,
		totalCapital:   config.TotalCapital,
		peakCapital:    config.TotalCapital,
		dailyLossLimit: config.DailyLossLimit,
		maxDrawdown:    config.MaxDrawdown,
		positionLimits: make(map[string]float64),
		lastResetDate:  time.Now(),
		tradeHistory:   make([]TradeRecord, 0),
	}

	if config.DailyTradeLimit > 0 {
		rm.dailyTradeCount = 0
	}

	return rm
}

func (rm *RiskManager) CanTrade() bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.resetDailyIfNeededLocked()

	if rm.tradingHalted {
		return false
	}

	if rm.dailyLossLimit > 0 && rm.dailyPnL <= -rm.dailyLossLimit {
		return false
	}

	if rm.maxDrawdown > 0 && rm.currentDrawdown > rm.maxDrawdown {
		return false
	}

	if rm.config.DailyTradeLimit > 0 && rm.dailyTradeCount >= rm.config.DailyTradeLimit {
		return false
	}

	if rm.config.MaxTradeCount > 0 && rm.tradeCount >= rm.config.MaxTradeCount {
		return false
	}

	equity := rm.totalCapital + rm.totalPnL
	if equity <= 0 || rm.usedCapital >= equity*0.9 {
		return false
	}

	return true
}

// ApproveOrder is the authoritative gate for a new order. It returns the
// maximum USD notional the caller is allowed to execute, capped by every risk
// limit. Strategies must size their actual order from this value; checking a
// separately calculated size would leave the real order unconstrained.
func (rm *RiskManager) ApproveOrder(signal *TradingSignal, requestedUSD float64) (float64, error) {
	if signal == nil {
		return 0, fmt.Errorf("signal is nil")
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.resetDailyIfNeededLocked()

	if rm.tradingHalted {
		return 0, fmt.Errorf("trading halted: %s", rm.haltReason)
	}
	if signal.Confidence < rm.config.MinConfidence {
		return 0, fmt.Errorf("confidence %.1f is below minimum %.1f", signal.Confidence, rm.config.MinConfidence)
	}
	if rm.dailyLossLimit > 0 && rm.dailyPnL <= -rm.dailyLossLimit {
		return 0, fmt.Errorf("daily loss limit reached")
	}
	if rm.maxDrawdown > 0 && rm.currentDrawdown > rm.maxDrawdown {
		return 0, fmt.Errorf("maximum drawdown exceeded")
	}
	if rm.config.DailyTradeLimit > 0 && rm.dailyTradeCount >= rm.config.DailyTradeLimit {
		return 0, fmt.Errorf("daily trade limit reached")
	}
	if rm.config.MaxTradeCount > 0 && rm.tradeCount >= rm.config.MaxTradeCount {
		return 0, fmt.Errorf("maximum trade count reached")
	}
	if requestedUSD <= 0 {
		return 0, fmt.Errorf("requested size must be positive")
	}

	equity := rm.totalCapital + rm.totalPnL
	if equity <= 0 {
		return 0, fmt.Errorf("account equity is not positive")
	}
	remainingCapital := equity - rm.usedCapital
	if remainingCapital <= 0 {
		return 0, fmt.Errorf("no available capital")
	}

	existingExposure := rm.positionLimits[signal.MarketID]
	if existingExposure <= 0 && rm.config.MaxOpenPositions > 0 && len(rm.positionLimits) >= rm.config.MaxOpenPositions {
		return 0, fmt.Errorf("maximum open positions (%d) reached", rm.config.MaxOpenPositions)
	}

	maxForMarket := rm.config.DiversificationLimit*equity - existingExposure
	if maxForMarket <= 0 {
		return 0, fmt.Errorf("diversification limit reached for market %s", signal.MarketID)
	}

	approved := requestedUSD
	if approved > rm.config.MaxPositionSize {
		approved = rm.config.MaxPositionSize
	}
	if positionCap := equity * rm.config.MaxPositionPercent; approved > positionCap {
		approved = positionCap
	}
	if approved > remainingCapital {
		approved = remainingCapital
	}
	if approved > maxForMarket {
		approved = maxForMarket
	}

	const minOrderUSD = 1.0
	if approved < minOrderUSD {
		return 0, fmt.Errorf("approved size %.2f is below the $%.2f minimum", approved, minOrderUSD)
	}
	return approved, nil
}

func (rm *RiskManager) CheckTradeLimits(signal *TradingSignal) bool {
	if signal == nil {
		return false
	}
	approved, err := rm.ApproveOrder(signal, rm.CalculatePositionSize(signal))
	return err == nil && approved > 0
}

func (rm *RiskManager) CalculatePositionSize(signal *TradingSignal) float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.calculatePositionSizeLocked(signal)
}

func (rm *RiskManager) calculatePositionSizeLocked(signal *TradingSignal) float64 {
	if signal == nil {
		return 0
	}
	if signal.Assessment != nil && signal.Assessment.Edge != 0 {
		kellyFraction := rm.calculateKellyFractionLocked(signal)
		kellySize := rm.totalCapital * kellyFraction

		maxSize := rm.config.MaxPositionSize
		if kellySize > maxSize {
			kellySize = maxSize
		}

		return kellySize
	}

	baseSize := rm.totalCapital * 0.01

	confidenceMultiplier := signal.Confidence / 100.0
	size := baseSize * confidenceMultiplier

	if size > rm.config.MaxPositionSize {
		size = rm.config.MaxPositionSize
	}

	return size
}

func (rm *RiskManager) calculateKellyFractionLocked(signal *TradingSignal) float64 {
	if signal == nil || signal.Assessment == nil {
		return 0
	}
	assessment := signal.Assessment
	aiProbability := assessment.AIProbability
	marketProbability := assessment.MarketImpliedProb
	if aiProbability < 0 || aiProbability > 1 || marketProbability <= 0 || marketProbability >= 1 {
		return 0
	}

	winProbability := aiProbability
	price := marketProbability
	if assessment.Edge < 0 {
		winProbability = 1 - aiProbability
		price = 1 - marketProbability
	}
	if winProbability <= price {
		return 0
	}

	kellyFraction := (winProbability - price) / (1 - price)
	kellyFraction *= rm.config.KellyMultiplier
	if kellyFraction > 0.25 {
		kellyFraction = 0.25
	}

	return kellyFraction
}

func (rm *RiskManager) RecordTrade(execution *TradeExecution) {
	if execution == nil || execution.Size <= 0 {
		return
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.resetDailyIfNeededLocked()

	// execution.Size is USD notional, so used capital is the notional itself,
	// not notional multiplied by a 0-1 contract price.
	rm.usedCapital += execution.Size
	rm.tradeCount++
	rm.dailyTradeCount++

	rm.tradeHistory = append(rm.tradeHistory, TradeRecord{
		MarketID:  execution.MarketID,
		Strategy:  execution.Strategy,
		Side:      execution.Side,
		Size:      execution.Size,
		Price:     execution.Price,
		Timestamp: execution.Timestamp,
	})

	rm.positionLimits[execution.MarketID] += execution.Size
}

// RecordSettlement releases the cost basis of a closed position and applies
// realized PnL. Without this, used capital grows forever and the bot silently
// stops trading after a few positions.
func (rm *RiskManager) RecordSettlement(marketID string, costUSD, realizedPnL float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.resetDailyIfNeededLocked()

	if costUSD > 0 {
		rm.usedCapital -= costUSD
		if rm.usedCapital < 0 {
			rm.usedCapital = 0
		}
		exposure := rm.positionLimits[marketID] - costUSD
		if exposure <= 1e-9 {
			delete(rm.positionLimits, marketID)
		} else {
			rm.positionLimits[marketID] = exposure
		}
	}

	if realizedPnL != 0 {
		rm.applyPnLLocked(realizedPnL)
		rm.tradeHistory = append(rm.tradeHistory, TradeRecord{
			MarketID:  marketID,
			Side:      "SETTLE",
			PnL:       realizedPnL,
			Timestamp: time.Now(),
		})
	}
}

func (rm *RiskManager) UpdatePnL(pnl float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.resetDailyIfNeededLocked()
	rm.applyPnLLocked(pnl)
}

func (rm *RiskManager) applyPnLLocked(pnl float64) {
	rm.totalPnL += pnl
	rm.dailyPnL += pnl

	newCapital := rm.totalCapital + rm.totalPnL

	if newCapital > rm.peakCapital {
		rm.peakCapital = newCapital
	}
	if rm.peakCapital > 0 {
		rm.currentDrawdown = (rm.peakCapital - newCapital) / rm.peakCapital
		if rm.currentDrawdown < 0 {
			rm.currentDrawdown = 0
		}
	}

	if rm.maxDrawdown > 0 && rm.currentDrawdown > rm.maxDrawdown {
		rm.tradingHalted = true
		rm.haltReason = "Maximum drawdown exceeded"
	}

	if rm.dailyLossLimit > 0 && rm.dailyPnL <= -rm.dailyLossLimit {
		rm.tradingHalted = true
		rm.haltReason = "Daily loss limit reached"
	}
}

func (rm *RiskManager) resetDailyIfNeededLocked() {
	now := time.Now()
	if rm.lastResetDate.IsZero() {
		rm.lastResetDate = now
		return
	}
	lastYear, lastMonth, lastDay := rm.lastResetDate.Date()
	year, month, day := now.Date()
	if lastYear != year || lastMonth != month || lastDay != day {
		rm.dailyPnL = 0
		rm.dailyTradeCount = 0
		rm.lastResetDate = now
		if rm.tradingHalted && rm.haltReason == "Daily loss limit reached" {
			rm.tradingHalted = false
			rm.haltReason = ""
		}
	}
}

func (rm *RiskManager) HaltTrading(reason string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.tradingHalted = true
	rm.haltReason = reason
}

func (rm *RiskManager) ResumeTrading() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.tradingHalted = false
	rm.haltReason = ""
}

func (rm *RiskManager) IsTradingHalted() (bool, string) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.tradingHalted, rm.haltReason
}

func (rm *RiskManager) ResetDailyStats() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.dailyPnL = 0
	rm.dailyTradeCount = 0
	rm.lastResetDate = time.Now()

	if rm.tradingHalted && rm.haltReason == "Daily loss limit reached" {
		rm.tradingHalted = false
		rm.haltReason = ""
	}
}

func (rm *RiskManager) GetRiskMetrics() *RiskMetrics {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	currentCapital := rm.totalCapital + rm.totalPnL
	availableCapital := currentCapital - rm.usedCapital
	if availableCapital < 0 {
		availableCapital = 0
	}
	riskUtilization := 0.0
	if currentCapital > 0 {
		riskUtilization = rm.usedCapital / currentCapital
	}

	return &RiskMetrics{
		TotalCapital:     rm.totalCapital,
		CurrentCapital:   currentCapital,
		UsedCapital:      rm.usedCapital,
		AvailableCapital: availableCapital,
		TotalPnL:         rm.totalPnL,
		DailyPnL:         rm.dailyPnL,
		PeakCapital:      rm.peakCapital,
		CurrentDrawdown:  rm.currentDrawdown,
		MaxDrawdown:      rm.maxDrawdown,
		TradingHalted:    rm.tradingHalted,
		HaltReason:       rm.haltReason,
		TradeCount:       rm.tradeCount,
		DailyTradeCount:  rm.dailyTradeCount,
		PositionCount:    len(rm.positionLimits),
		RiskUtilization:  riskUtilization,
	}
}

type RiskMetrics struct {
	TotalCapital     float64 `json:"total_capital"`
	CurrentCapital   float64 `json:"current_capital"`
	UsedCapital      float64 `json:"used_capital"`
	AvailableCapital float64 `json:"available_capital"`
	TotalPnL         float64 `json:"total_pnl"`
	DailyPnL         float64 `json:"daily_pnl"`
	PeakCapital      float64 `json:"peak_capital"`
	CurrentDrawdown  float64 `json:"current_drawdown"`
	MaxDrawdown      float64 `json:"max_drawdown"`
	TradingHalted    bool    `json:"trading_halted"`
	HaltReason       string  `json:"halt_reason"`
	TradeCount       int     `json:"trade_count"`
	DailyTradeCount  int     `json:"daily_trade_count"`
	PositionCount    int     `json:"position_count"`
	RiskUtilization  float64 `json:"risk_utilization"`
}

func (rm *RiskManager) GetTradeHistory(limit int) []TradeRecord {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if limit <= 0 || limit > len(rm.tradeHistory) {
		limit = len(rm.tradeHistory)
	}

	start := len(rm.tradeHistory) - limit
	if start < 0 {
		start = 0
	}

	history := make([]TradeRecord, limit)
	copy(history, rm.tradeHistory[start:])

	return history
}

func (rm *RiskManager) GetPositionExposure(marketID string) float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.positionLimits[marketID]
}

func (rm *RiskManager) GetTotalExposure() float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	total := 0.0
	for _, exposure := range rm.positionLimits {
		total += exposure
	}
	return total
}

func (rm *RiskManager) UpdateCapital(newCapital float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.totalCapital = newCapital
	if newCapital > rm.peakCapital {
		rm.peakCapital = newCapital
	}
}

func (rm *RiskManager) GetConfig() *RiskConfig {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	if rm.config == nil {
		return nil
	}
	copy := *rm.config
	return &copy
}

func (rm *RiskManager) UpdateConfig(config *RiskConfig) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.config = config
}

func (rm *RiskManager) CalculateSharpeRatio() float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if len(rm.tradeHistory) < 2 {
		return 0
	}

	var returns []float64
	for i := 1; i < len(rm.tradeHistory); i++ {
		pnl := rm.tradeHistory[i].PnL
		cost := rm.tradeHistory[i].Size * rm.tradeHistory[i].Price
		if cost > 0 {
			returns = append(returns, pnl/cost)
		}
	}

	if len(returns) == 0 {
		return 0
	}

	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))

	variance := 0.0
	for _, r := range returns {
		diff := r - mean
		variance += diff * diff
	}
	variance /= float64(len(returns))

	stdDev := math.Sqrt(variance)

	if stdDev == 0 {
		return 0
	}

	riskFreeRate := 0.02 / 252
	sharpe := (mean - riskFreeRate) / stdDev

	return sharpe * math.Sqrt(252)
}

func (rm *RiskManager) GetWinRate() float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if len(rm.tradeHistory) == 0 {
		return 0
	}

	wins := 0
	for _, trade := range rm.tradeHistory {
		if trade.PnL > 0 {
			wins++
		}
	}

	return float64(wins) / float64(len(rm.tradeHistory)) * 100
}

func (rm *RiskManager) GetProfitFactor() float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	grossProfit := 0.0
	grossLoss := 0.0

	for _, trade := range rm.tradeHistory {
		if trade.PnL > 0 {
			grossProfit += trade.PnL
		} else {
			grossLoss += math.Abs(trade.PnL)
		}
	}

	if grossLoss == 0 {
		if grossProfit > 0 {
			return math.Inf(1)
		}
		return 0
	}

	return grossProfit / grossLoss
}
