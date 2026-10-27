package polymarket

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

type MarketAnalyzer struct {
	client *Client
	gamma  *GammaService
	data   *DataService
	clob   *CLOBService
	cache  *MarketCache
}

type MarketCache struct {
	mu         sync.RWMutex
	prices     map[string]*MarketPrice
	orderbooks map[string]*OrderBook
	lastUpdate map[string]time.Time
	ttl        time.Duration
}

type MarketOpportunity struct {
	MarketID       string  `json:"market_id"`
	MarketTitle    string  `json:"market_title"`
	Strategy       string  `json:"strategy"`
	ExpectedValue  float64 `json:"expected_value"`
	Confidence     float64 `json:"confidence"`
	RecommendedBet string  `json:"recommended_bet"`
	BetSize        float64 `json:"bet_size"`
	Reasoning      string  `json:"reasoning"`
	RiskLevel      string  `json:"risk_level"`
}

type LiquidityAnalysis struct {
	MarketID         string  `json:"market_id"`
	BidLiquidity     float64 `json:"bid_liquidity"`
	AskLiquidity     float64 `json:"ask_liquidity"`
	TotalLiquidity   float64 `json:"total_liquidity"`
	BidAskSpread     float64 `json:"bid_ask_spread"`
	SpreadPercent    float64 `json:"spread_percent"`
	LiquidityScore   float64 `json:"liquidity_score"`
	CanExecuteLarge  bool    `json:"can_execute_large"`
	SlippageEstimate float64 `json:"slippage_estimate"`
}

type VolumeAnalysis struct {
	MarketID      string  `json:"market_id"`
	Volume24h     float64 `json:"volume_24h"`
	Volume7d      float64 `json:"volume_7d"`
	Volume30d     float64 `json:"volume_30d"`
	VolumeTrend   string  `json:"volume_trend"`
	VolumeGrowth  float64 `json:"volume_growth"`
	ActivityLevel string  `json:"activity_level"`
}

type OrderBookImbalance struct {
	MarketID         string  `json:"market_id"`
	BidVolume        float64 `json:"bid_volume"`
	AskVolume        float64 `json:"ask_volume"`
	Imbalance        float64 `json:"imbalance"`
	ImbalancePercent float64 `json:"imbalance_percent"`
	Pressure         string  `json:"pressure"`
	Signal           string  `json:"signal"`
}

type PricePattern struct {
	MarketID    string  `json:"market_id"`
	Pattern     string  `json:"pattern"`
	Strength    float64 `json:"strength"`
	Direction   string  `json:"direction"`
	TargetPrice float64 `json:"target_price"`
	Confidence  float64 `json:"confidence"`
	Timeframe   string  `json:"timeframe"`
}

func NewMarketAnalyzer(client *Client) *MarketAnalyzer {
	return &MarketAnalyzer{
		client: client,
		gamma:  NewGammaService(client),
		data:   NewDataService(client),
		clob:   NewCLOBService(client),
		cache: &MarketCache{
			prices:     make(map[string]*MarketPrice),
			orderbooks: make(map[string]*OrderBook),
			lastUpdate: make(map[string]time.Time),
			ttl:        5 * time.Second,
		},
	}
}

func (ma *MarketAnalyzer) AnalyzeMarket(ctx context.Context, marketID string) (*MarketOpportunity, error) {
	market, err := ma.gamma.GetMarketByID(ctx, marketID)
	if err != nil {
		return nil, fmt.Errorf("failed to get market: %w", err)
	}
	if market == nil {
		return nil, fmt.Errorf("market %s returned no data", marketID)
	}

	yesTokenID, _, err := market.BinaryTokenIDs()
	if err != nil {
		return nil, err
	}

	orderBook, err := ma.clob.GetOrderBook(ctx, yesTokenID)
	if err != nil {
		return nil, fmt.Errorf("failed to get orderbook: %w", err)
	}

	price, err := ma.clob.GetMarketPrice(ctx, yesTokenID)
	if err != nil {
		return nil, fmt.Errorf("failed to get price: %w", err)
	}
	if price == nil {
		return nil, fmt.Errorf("market %s returned no price data", marketID)
	}

	liquidity := ma.AnalyzeLiquidity(orderBook)
	volume := ma.AnalyzeVolumeFromMarket(market, marketID)

	imbalance := ma.AnalyzeOrderBookImbalance(orderBook)

	title := market.Title
	if title == "" {
		title = market.Question
	}
	opportunity := &MarketOpportunity{
		MarketID:    marketID,
		MarketTitle: title,
	}

	if liquidity.LiquidityScore < 30 {
		opportunity.RiskLevel = "HIGH"
		opportunity.Reasoning = "Low liquidity - high slippage risk"
		return opportunity, nil
	}

	impliedProb := clampProbability(price.YesPrice)
	fairValue := ma.CalculateFairValue(market, price, volume, imbalance)

	if math.Abs(fairValue-impliedProb) > 0.1 {
		if fairValue > impliedProb {
			opportunity.Strategy = "VALUE_BET_YES"
			opportunity.RecommendedBet = "YES"
			opportunity.ExpectedValue = fairValue - impliedProb
			opportunity.Reasoning = fmt.Sprintf("Fair value %.2f > Implied prob %.2f", fairValue, impliedProb)
		} else {
			opportunity.Strategy = "VALUE_BET_NO"
			opportunity.RecommendedBet = "NO"
			opportunity.ExpectedValue = impliedProb - fairValue
			opportunity.Reasoning = fmt.Sprintf("Fair value %.2f < Implied prob %.2f", fairValue, impliedProb)
		}
		opportunity.Confidence = math.Abs(fairValue-impliedProb) * 100
	} else {
		opportunity.Strategy = "HOLD"
		opportunity.Reasoning = "No probability edge detected; order book imbalance alone is not arbitrage"
	}

	if opportunity.Confidence > 70 {
		opportunity.RiskLevel = "LOW"
	} else if opportunity.Confidence > 40 {
		opportunity.RiskLevel = "MEDIUM"
	} else {
		opportunity.RiskLevel = "HIGH"
	}

	opportunity.BetSize = ma.CalculateBetSize(opportunity.ExpectedValue, opportunity.Confidence, liquidity)

	return opportunity, nil
}

func (ma *MarketAnalyzer) AnalyzeLiquidity(ob *OrderBook) *LiquidityAnalysis {
	if ob == nil {
		return &LiquidityAnalysis{LiquidityScore: 0}
	}

	var bidLiq, askLiq float64
	for _, bid := range ob.Bids {
		bidLiq += bid.Size * bid.Price
	}
	for _, ask := range ob.Asks {
		askLiq += ask.Size * ask.Price
	}

	totalLiq := bidLiq + askLiq

	var spread, spreadPercent float64
	if len(ob.Bids) > 0 && len(ob.Asks) > 0 {
		spread = ob.Asks[0].Price - ob.Bids[0].Price
		midPrice := (ob.Asks[0].Price + ob.Bids[0].Price) / 2
		if midPrice > 0 {
			spreadPercent = (spread / midPrice) * 100
		}
	}

	score := ma.calculateLiquidityScore(totalLiq, spreadPercent)

	return &LiquidityAnalysis{
		MarketID:         ob.MarketID,
		BidLiquidity:     bidLiq,
		AskLiquidity:     askLiq,
		TotalLiquidity:   totalLiq,
		BidAskSpread:     spread,
		SpreadPercent:    spreadPercent,
		LiquidityScore:   score,
		CanExecuteLarge:  totalLiq > 10000,
		SlippageEstimate: ma.estimateSlippage(totalLiq, 1000),
	}
}

func (ma *MarketAnalyzer) calculateLiquidityScore(totalLiq, spreadPercent float64) float64 {
	volumeScore := math.Min(totalLiq/10000*50, 50)
	spreadScore := math.Max(0, 50-spreadPercent*5)
	return volumeScore + spreadScore
}

func (ma *MarketAnalyzer) estimateSlippage(totalLiq, tradeSize float64) float64 {
	if totalLiq == 0 {
		return 100
	}
	return (tradeSize / totalLiq) * 100 * 2
}

func (ma *MarketAnalyzer) AnalyzeVolumeFromMarket(market *Market, marketID string) *VolumeAnalysis {
	vol24h := 0.0
	if market != nil {
		vol24h = market.Volume24h
		if vol24h <= 0 {
			vol24h = market.Volume
		}
	}

	activity := "LOW"
	if vol24h > 100000 {
		activity = "VERY_HIGH"
	} else if vol24h > 50000 {
		activity = "HIGH"
	} else if vol24h > 10000 {
		activity = "MEDIUM"
	}

	return &VolumeAnalysis{
		MarketID:      marketID,
		Volume24h:     vol24h,
		VolumeTrend:   "UNKNOWN",
		ActivityLevel: activity,
	}
}

func (ma *MarketAnalyzer) AnalyzeVolume(ctx context.Context, marketID string) (*VolumeAnalysis, error) {
	market, err := ma.gamma.GetMarketByID(ctx, marketID)
	if err != nil {
		return nil, err
	}
	return ma.AnalyzeVolumeFromMarket(market, marketID), nil
}

func (ma *MarketAnalyzer) AnalyzeOrderBookImbalance(ob *OrderBook) *OrderBookImbalance {
	if ob == nil {
		return &OrderBookImbalance{}
	}
	if len(ob.Bids) == 0 && len(ob.Asks) == 0 {
		return &OrderBookImbalance{MarketID: ob.MarketID}
	}

	var bidVol, askVol float64
	for _, bid := range ob.Bids {
		bidVol += bid.Size
	}
	for _, ask := range ob.Asks {
		askVol += ask.Size
	}

	total := bidVol + askVol
	if total == 0 {
		return &OrderBookImbalance{MarketID: ob.MarketID}
	}

	imbalance := bidVol - askVol
	imbalancePercent := (imbalance / total) * 100

	pressure := "NEUTRAL"
	signal := "HOLD"

	if imbalancePercent > 20 {
		pressure = "BUYING"
		signal = "BULLISH"
	} else if imbalancePercent < -20 {
		pressure = "SELLING"
		signal = "BEARISH"
	}

	return &OrderBookImbalance{
		MarketID:         ob.MarketID,
		BidVolume:        bidVol,
		AskVolume:        askVol,
		Imbalance:        imbalance,
		ImbalancePercent: imbalancePercent,
		Pressure:         pressure,
		Signal:           signal,
	}
}

// CalculateFairValue deliberately does NOT invent an edge from price, volume
// or order-book imbalance. Those heuristics are not calibrated forecasts and
// previously could manufacture a 0.5 "edge" from book imbalance alone. Use
// AIPredictor + a validated calibration layer to supply a real probability;
// otherwise the fair value is simply the market-implied probability.
func (ma *MarketAnalyzer) CalculateFairValue(market *Market, price *MarketPrice, volume *VolumeAnalysis, imbalance *OrderBookImbalance) float64 {
	_ = market
	_ = volume
	_ = imbalance
	if price == nil {
		return 0.5
	}
	return clampProbability(price.YesPrice)
}

func (ma *MarketAnalyzer) CalculateBetSize(expectedValue float64, confidence float64, liquidity *LiquidityAnalysis) float64 {
	if expectedValue <= 0 {
		return 0
	}

	baseSize := 100.0

	if confidence > 70 {
		baseSize *= 2.0
	} else if confidence > 50 {
		baseSize *= 1.5
	}

	if liquidity != nil {
		maxSize := liquidity.TotalLiquidity * 0.01
		if baseSize > maxSize {
			baseSize = maxSize
		}
	}

	return baseSize
}

func (ma *MarketAnalyzer) ScanMarkets(ctx context.Context, marketIDs []string) ([]*MarketOpportunity, error) {
	var opportunities []*MarketOpportunity
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(marketIDs))

	for _, marketID := range marketIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			opp, err := ma.AnalyzeMarket(ctx, id)
			if err != nil {
				errChan <- fmt.Errorf("market %s: %w", id, err)
				return
			}
			if opp.ExpectedValue > 0.05 || opp.Confidence > 50 {
				mu.Lock()
				opportunities = append(opportunities, opp)
				mu.Unlock()
			}
		}(marketID)
	}

	wg.Wait()
	close(errChan)

	var scanErrors []error
	for err := range errChan {
		scanErrors = append(scanErrors, err)
	}

	sort.Slice(opportunities, func(i, j int) bool {
		return opportunities[i].ExpectedValue > opportunities[j].ExpectedValue
	})

	if len(opportunities) == 0 && len(scanErrors) > 0 {
		return nil, errors.Join(scanErrors...)
	}

	return opportunities, nil
}

func (ma *MarketAnalyzer) DetectPricePatterns(ctx context.Context, marketID string, interval string, limit int) (*PricePattern, error) {
	params := &GetPriceHistoryParams{
		MarketID: marketID,
		Interval: interval,
		Limit:    limit,
		Fidelity: 60,
	}

	history, err := ma.clob.GetPriceHistory(ctx, params)
	if err != nil {
		return nil, err
	}

	if len(history.Prices) < 10 {
		return nil, fmt.Errorf("insufficient data")
	}

	prices := make([]float64, len(history.Prices))
	for i, p := range history.Prices {
		prices[i] = p.Price
	}

	pattern, strength, direction := ma.identifyPattern(prices)

	var targetPrice float64
	if direction == "UP" {
		targetPrice = prices[len(prices)-1] * 1.1
	} else {
		targetPrice = prices[len(prices)-1] * 0.9
	}

	return &PricePattern{
		MarketID:    marketID,
		Pattern:     pattern,
		Strength:    strength,
		Direction:   direction,
		TargetPrice: targetPrice,
		Confidence:  strength * 100,
		Timeframe:   interval,
	}, nil
}

func (ma *MarketAnalyzer) identifyPattern(prices []float64) (string, float64, string) {
	n := len(prices)
	if n < 5 {
		return "UNKNOWN", 0, "NEUTRAL"
	}

	trend := ma.calculateTrend(prices)
	volatility := ma.calculateVolatility(prices)

	if trend > 0.02 {
		return "UPTREND", trend * 100, "UP"
	} else if trend < -0.02 {
		return "DOWNTREND", -trend * 100, "DOWN"
	}

	if volatility < 0.01 {
		return "CONSOLIDATION", 1 - volatility*10, "NEUTRAL"
	}

	return "RANGEBound", 0.5, "NEUTRAL"
}

func (ma *MarketAnalyzer) calculateTrend(prices []float64) float64 {
	n := len(prices)
	if n < 2 || prices[0] <= 0 {
		return 0
	}
	return (prices[n-1] - prices[0]) / prices[0]
}

func (ma *MarketAnalyzer) calculateVolatility(prices []float64) float64 {
	if len(prices) < 2 {
		return 0
	}

	mean := 0.0
	for _, p := range prices {
		mean += p
	}
	mean /= float64(len(prices))

	variance := 0.0
	for _, p := range prices {
		diff := p - mean
		variance += diff * diff
	}
	variance /= float64(len(prices))

	if mean == 0 {
		return 0
	}
	return math.Sqrt(variance) / mean
}
