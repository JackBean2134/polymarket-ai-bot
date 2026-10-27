package polymarket

import (
	"math"
	"time"
)

const logitEpsilon = 1e-6

// PriceFeatures are computed in logit space so that probabilities near 0 or 1
// are not compared on the misleading absolute-price scale.
type PriceFeatures struct {
	Last        float64 `json:"last"`
	Logit       float64 `json:"logit"`
	LastChange  float64 `json:"last_change"`
	ROC         float64 `json:"roc_logit"`
	ATR         float64 `json:"atr_logit"`
	EMA         float64 `json:"ema"`
	AroonUp     float64 `json:"aroon_up"`
	AroonDown   float64 `json:"aroon_down"`
	AroonOsc    float64 `json:"aroon_osc"`
	SampleCount int     `json:"sample_count"`
}

type OrderBookFeatures struct {
	BestBid   float64 `json:"best_bid"`
	BestAsk   float64 `json:"best_ask"`
	Spread    float64 `json:"spread"`
	SpreadPct float64 `json:"spread_pct"`
	BidDepth  float64 `json:"bid_depth_shares"`
	AskDepth  float64 `json:"ask_depth_shares"`
	Imbalance float64 `json:"imbalance"`
}

func logit(probability float64) float64 {
	p := probability
	if p < logitEpsilon {
		p = logitEpsilon
	}
	if p > 1-logitEpsilon {
		p = 1 - logitEpsilon
	}
	return math.Log(p / (1 - p))
}

func ComputePriceFeatures(prices []float64, rocPeriod, atrPeriod, emaPeriod, aroonPeriod int) PriceFeatures {
	if rocPeriod <= 0 {
		rocPeriod = 6
	}
	if atrPeriod <= 0 {
		atrPeriod = 14
	}
	if emaPeriod <= 0 {
		emaPeriod = 50
	}
	if aroonPeriod <= 0 {
		aroonPeriod = 25
	}

	clean := make([]float64, 0, len(prices))
	for _, price := range prices {
		if price > 0 && price < 1 && isFinite(price) {
			clean = append(clean, price)
		}
	}
	if len(clean) == 0 {
		return PriceFeatures{}
	}

	logits := make([]float64, len(clean))
	for i, price := range clean {
		logits[i] = logit(price)
	}

	features := PriceFeatures{
		Last:        clean[len(clean)-1],
		Logit:       logits[len(logits)-1],
		SampleCount: len(clean),
	}
	if len(logits) >= 2 {
		features.LastChange = logits[len(logits)-1] - logits[len(logits)-2]
	}
	if len(logits) > rocPeriod {
		features.ROC = logits[len(logits)-1] - logits[len(logits)-1-rocPeriod]
	}
	features.ATR = meanAbsChange(logits, atrPeriod)
	features.EMA = ema(clean, emaPeriod)
	features.AroonUp, features.AroonDown = aroon(clean, aroonPeriod)
	features.AroonOsc = features.AroonUp - features.AroonDown
	return features
}

func meanAbsChange(values []float64, period int) float64 {
	if len(values) < 2 || period < 1 {
		return 0
	}
	start := len(values) - period
	if start < 1 {
		start = 1
	}
	sum := 0.0
	count := 0
	for i := start; i < len(values); i++ {
		sum += math.Abs(values[i] - values[i-1])
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func ema(values []float64, period int) float64 {
	if len(values) == 0 {
		return 0
	}
	if period <= 1 {
		return values[len(values)-1]
	}
	alpha := 2.0 / (float64(period) + 1)
	value := values[0]
	for _, price := range values[1:] {
		value = alpha*price + (1-alpha)*value
	}
	return value
}

func aroon(values []float64, period int) (up, down float64) {
	if len(values) < 2 || period < 2 {
		return 50, 50
	}
	if period > len(values) {
		period = len(values)
	}
	window := values[len(values)-period:]
	highIndex, lowIndex := 0, 0
	for i := 1; i < len(window); i++ {
		if window[i] >= window[highIndex] {
			highIndex = i
		}
		if window[i] <= window[lowIndex] {
			lowIndex = i
		}
	}
	periodsSinceHigh := len(window) - 1 - highIndex
	periodsSinceLow := len(window) - 1 - lowIndex
	up = 100 * float64(period-periodsSinceHigh) / float64(period)
	down = 100 * float64(period-periodsSinceLow) / float64(period)
	return up, down
}

func ComputeOrderBookFeatures(book *OrderBook, levels int) OrderBookFeatures {
	if book == nil {
		return OrderBookFeatures{}
	}
	if levels <= 0 {
		levels = 10
	}
	var features OrderBookFeatures
	for i, bid := range book.Bids {
		if i >= levels {
			break
		}
		if bid.Price > 0 && bid.Size > 0 {
			if features.BestBid == 0 || bid.Price > features.BestBid {
				features.BestBid = bid.Price
			}
			features.BidDepth += bid.Size
		}
	}
	for i, ask := range book.Asks {
		if i >= levels {
			break
		}
		if ask.Price > 0 && ask.Size > 0 {
			if features.BestAsk == 0 || ask.Price < features.BestAsk {
				features.BestAsk = ask.Price
			}
			features.AskDepth += ask.Size
		}
	}
	if features.BestBid > 0 && features.BestAsk > 0 {
		features.Spread = features.BestAsk - features.BestBid
		features.SpreadPct = features.Spread / ((features.BestBid + features.BestAsk) / 2)
	}
	total := features.BidDepth + features.AskDepth
	if total > 0 {
		features.Imbalance = (features.BidDepth - features.AskDepth) / total
	}
	return features
}

// MomentumRules mirror the checkable entry rules from the article. They are a
// research filter, not a forecast: a passing signal still needs a calibrated
// model probability and a positive cost-adjusted edge.
type MomentumRules struct {
	MinPrice         float64
	MaxPrice         float64
	ROCLogitPeriod   int
	ATRPeriod        int
	EMAPeriod        int
	AroonPeriod      int
	MinMoveATR       float64
	MinDepthMultiple float64
	MaxSpreadPct     float64
	MinTimeToExpiry  time.Duration
}

func DefaultMomentumRules() MomentumRules {
	return MomentumRules{
		MinPrice:         0.15,
		MaxPrice:         0.85,
		ROCLogitPeriod:   6,
		ATRPeriod:        14,
		EMAPeriod:        50,
		AroonPeriod:      25,
		MinMoveATR:       0.8,
		MinDepthMultiple: 10,
		MaxSpreadPct:     0.05,
		MinTimeToExpiry:  2 * time.Hour,
	}
}

type MomentumDecision struct {
	Enter   bool     `json:"enter"`
	Reasons []string `json:"reasons"`
}

func EvaluateMomentumEntry(price PriceFeatures, book OrderBookFeatures, plannedUSD float64, timeToExpiry time.Duration, rules MomentumRules) MomentumDecision {
	decision := MomentumDecision{}
	fail := func(reason string) {
		decision.Reasons = append(decision.Reasons, reason)
	}

	if price.SampleCount == 0 {
		fail("missing_price_history")
	}
	if price.Last < rules.MinPrice || price.Last > rules.MaxPrice {
		fail("price_outside_band")
	}
	if price.ROC <= 0 {
		fail("roc_logit_not_positive")
	}
	if price.ATR <= 0 || math.Abs(price.LastChange) < rules.MinMoveATR*price.ATR {
		fail("move_below_atr_filter")
	}
	if price.EMA <= 0 || price.Last <= price.EMA {
		fail("price_not_above_ema")
	}
	if timeToExpiry < rules.MinTimeToExpiry {
		fail("insufficient_time_to_expiry")
	}
	if book.SpreadPct > rules.MaxSpreadPct {
		fail("spread_too_wide")
	}
	if plannedUSD > 0 && price.Last > 0 {
		plannedShares := plannedUSD / price.Last
		if book.AskDepth < plannedShares*rules.MinDepthMultiple {
			fail("insufficient_book_depth")
		}
	}

	decision.Enter = len(decision.Reasons) == 0
	return decision
}
