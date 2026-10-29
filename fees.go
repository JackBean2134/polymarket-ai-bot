package polymarket

import (
	"math"
	"strings"
)

// TakerFeeUSD implements Polymarket's documented taker fee:
//
//	fee = shares × rate × p^exponent × (1 - p)^exponent
//
// The published formula uses exponent 1, giving fee = shares × rate × p × (1-p),
// which is symmetric around 50¢. Makers are never charged; `takerOnly` is true
// for every schedule currently published.
func TakerFeeUSD(price, shares, rate, exponent float64) float64 {
	if shares <= 0 || rate <= 0 || price <= 0 || price >= 1 {
		return 0
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0
	}
	if exponent <= 0 || math.IsNaN(exponent) || math.IsInf(exponent, 0) {
		exponent = 1
	}
	return shares * rate * math.Pow(price, exponent) * math.Pow(1-price, exponent)
}

// categoryTakerFeeRates mirrors the official fee table. It is only used when a
// market payload does not carry a feeSchedule; the schedule always wins.
var categoryTakerFeeRates = map[string]float64{
	"sports":      0.05,
	"politics":    0.04,
	"elections":   0.04,
	"crypto":      0.07,
	"economy":     0.05,
	"economics":   0.05,
	"finance":     0.04,
	"geopolitics": 0,
	"world":       0,
	"culture":     0.05,
	"tech":        0.04,
	"business":    0.05,
	"science":     0.05,
	"health":      0.05,
	"climate":     0.05,
	"weather":     0.05,
	"mentions":    0.04,
	"other":       0.05,
}

// defaultTakerFeeRate is the "Other / General" rate.
const defaultTakerFeeRate = 0.05

// DefaultTakerFeeRate returns the official rate for a category name. Geopolitics
// and world events are fee-free; unknown categories fall back to "Other".
func DefaultTakerFeeRate(category string) float64 {
	key := strings.ToLower(strings.TrimSpace(category))
	if rate, ok := categoryTakerFeeRates[key]; ok {
		return rate
	}
	return defaultTakerFeeRate
}

// MarketFeeRate resolves the taker fee rate for a market. An explicit
// feeSchedule always wins, including an explicit zero (zero_fees markets);
// only when no schedule exists does the category table apply.
func MarketFeeRate(market Market, category string) float64 {
	if market.FeeRateKnown {
		if math.IsNaN(market.FeeRate) || market.FeeRate <= 0 {
			return 0
		}
		if market.FeeRate > 1 {
			return 1
		}
		return market.FeeRate
	}
	if market.FeeRate > 0 {
		if market.FeeRate > 1 {
			return 1
		}
		return market.FeeRate
	}
	return DefaultTakerFeeRate(category)
}

// MarketFeeExponent returns the schedule exponent, defaulting to 1.
func MarketFeeExponent(market Market) float64 {
	if market.FeeExponent > 0 {
		return market.FeeExponent
	}
	return 1
}

// FeeModel describes how a paper or live simulator should charge taker fees.
// Rate/Exponent follow the protocol formula; Bps is the legacy approximation
// kept for callers that only know a basis-point number.
type FeeModel struct {
	Bps      int
	Rate     float64
	Exponent float64
}

// FeeForLevel charges the protocol fee for one filled price level.
func (m FeeModel) FeeForLevel(price, shares float64) float64 {
	if m.Rate > 0 {
		return TakerFeeUSD(price, shares, m.Rate, m.Exponent)
	}
	if m.Bps > 0 {
		return price * shares * float64(m.Bps) / 10000
	}
	return 0
}
