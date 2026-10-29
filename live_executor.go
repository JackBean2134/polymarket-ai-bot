package polymarket

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FeeScheduleResolver resolves a token ID to its Gamma market metadata, which
// is the only source of the protocol feeSchedule. *GammaService implements it.
type FeeScheduleResolver interface {
	GetMarketByTokenID(ctx context.Context, tokenID string) (*Market, error)
}

// LiveExecutor signs and submits a real CLOB order, then reconciles the fill.
// It is deliberately conservative: hard USD cap, FAK order type by default,
// no resting orders left behind after the reconciliation window.
type LiveExecutor struct {
	clob          *CLOBService
	signer        *OrderSigner
	orderType     string
	maxOrderUSD   float64
	pollTimeout   time.Duration
	stopFile      string
	allowedTokens []string
	feeResolver   FeeScheduleResolver
}

func NewLiveExecutor(clob *CLOBService, signer *OrderSigner, orderType string, maxOrderUSD float64) (*LiveExecutor, error) {
	if clob == nil || clob.client == nil {
		return nil, fmt.Errorf("CLOB client is required")
	}
	if signer == nil {
		return nil, fmt.Errorf("order signer is required")
	}
	if maxOrderUSD <= 0 {
		return nil, fmt.Errorf("max order USD must be positive")
	}
	if !clob.client.HasL2Credentials() {
		return nil, fmt.Errorf("API key, secret, passphrase and wallet address are required")
	}
	if !strings.EqualFold(clob.client.walletAddress, signer.Address()) {
		return nil, fmt.Errorf("signer address %s does not match POLY_ADDRESS %s", signer.Address(), clob.client.walletAddress)
	}
	orderType = strings.ToUpper(strings.TrimSpace(orderType))
	if orderType == "" {
		orderType = "FAK"
	}
	switch orderType {
	case "GTC", "FOK", "FAK":
	default:
		return nil, fmt.Errorf("unsupported order type %q (use GTC, FOK or FAK)", orderType)
	}
	return &LiveExecutor{
		clob:        clob,
		signer:      signer,
		orderType:   orderType,
		maxOrderUSD: maxOrderUSD,
		pollTimeout: 8 * time.Second,
	}, nil
}

func (l *LiveExecutor) IsPaper() bool {
	return false
}

func (l *LiveExecutor) SetStopFile(path string) {
	l.stopFile = path
}

func (l *LiveExecutor) SetAllowedTokens(tokens []string) {
	l.allowedTokens = append([]string(nil), tokens...)
}

// SetFeeResolver enables protocol fee estimation. Without it (and without an
// explicit request fee rate) Execute refuses to size or price an order,
// because the legacy takerBaseFee is not the fee rate.
func (l *LiveExecutor) SetFeeResolver(resolver FeeScheduleResolver) {
	l.feeResolver = resolver
}

func (l *LiveExecutor) Cancel(ctx context.Context, orderID string) error {
	return l.clob.CancelOrder(ctx, orderID)
}

func (l *LiveExecutor) Execute(ctx context.Context, request OrderRequest) (*ExecutionFill, error) {
	if err := CheckStopFile(l.stopFile); err != nil {
		return nil, err
	}
	if len(l.allowedTokens) == 0 {
		return nil, fmt.Errorf("live token whitelist is empty; refusing to send")
	}
	if !containsToken(l.allowedTokens, request.TokenID) {
		return nil, fmt.Errorf("token %s is not in the live whitelist", request.TokenID)
	}
	if strings.TrimSpace(request.TokenID) == "" {
		return nil, fmt.Errorf("token ID is required")
	}
	if request.Shares <= 0 || !validPrice(request.LimitPrice) {
		return nil, fmt.Errorf("shares and limit price must be positive")
	}
	side := strings.ToUpper(strings.TrimSpace(request.Side))
	if side != "BUY" && side != "SELL" {
		return nil, fmt.Errorf("side must be BUY or SELL")
	}

	tick, err := l.clob.GetTickSize(ctx, request.TokenID)
	if err != nil {
		return nil, fmt.Errorf("get tick size: %w", err)
	}
	tickSize := tick.TickSize
	if tickSize <= 0 {
		tickSize = 0.001
	}

	negRisk, err := l.clob.GetNegRisk(ctx, request.TokenID)
	if err != nil {
		return nil, fmt.Errorf("get neg-risk flag: %w", err)
	}
	feeRateBps, err := l.clob.GetFeeRateBps(ctx, request.TokenID)
	if err != nil {
		return nil, fmt.Errorf("get fee rate: %w", err)
	}

	maxCash := l.maxOrderUSD
	if request.MaxCashUSD > 0 && request.MaxCashUSD < maxCash {
		maxCash = request.MaxCashUSD
	}

	feeModel, err := l.resolveFeeModel(ctx, request)
	if err != nil {
		return nil, err
	}

	effectiveShares := request.Shares
	if maxShares := maxSharesForCash(maxCash, request.LimitPrice, feeModel) * 0.999; effectiveShares > maxShares {
		effectiveShares = maxShares
	}
	if effectiveShares <= 0 {
		return nil, fmt.Errorf("approved cash limit is below one share")
	}

	// Budget the protocol fee inside the hard cap.
	totalCost := effectiveShares * (request.LimitPrice + feeModel.FeeForLevel(request.LimitPrice, 1))
	if totalCost > maxCash+1e-9 {
		return nil, fmt.Errorf("order cost including fees %.2f exceeds the cash cap %.2f", totalCost, maxCash)
	}

	order, err := l.signer.BuildSignedOrder(request.TokenID, side, effectiveShares, request.LimitPrice, tickSize, feeRateBps, negRisk)
	if err != nil {
		return nil, err
	}

	post, err := l.clob.PostOrder(ctx, l.clob.client.APIKey(), l.orderType, order)
	if err != nil {
		return nil, err
	}
	if post.OrderID == "" {
		return nil, fmt.Errorf("exchange accepted no order ID: %s", post.ErrorMsg)
	}

	matchedShares := parseResponseShares(side, post.TakingAmount, post.MakingAmount)
	status := strings.ToUpper(post.Status)
	var lastOrder *LiveOrder

	// Prefer the order endpoint's size_matched over POST taking/making amounts.
	if matchedShares > 0 || isTerminalOrderStatus(status) || isMatchedStatus(status) {
		if liveOrder, err := l.clob.GetOrder(ctx, post.OrderID); err == nil {
			lastOrder = liveOrder
			status = strings.ToUpper(liveOrder.Status)
			if matched := liveOrder.SizeMatched.Float64(); matched > 0 {
				matchedShares = matched
			}
		}
	}

	deadline := time.Now().Add(l.pollTimeout)
	for {
		if matchedShares > 0 && isTerminalOrderStatus(status) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}

		liveOrder, err := l.clob.GetOrder(ctx, post.OrderID)
		if err != nil {
			continue
		}
		lastOrder = liveOrder
		status = strings.ToUpper(liveOrder.Status)
		if matched := liveOrder.SizeMatched.Float64(); matched > 0 {
			matchedShares = matched
		}
		if isTerminalOrderStatus(status) {
			break
		}
	}

	if matchedShares <= 0 && lastOrder != nil {
		matchedShares = lastOrder.SizeMatched.Float64()
	}
	if matchedShares <= 0 && isMatchedStatus(status) {
		return nil, fmt.Errorf("order %s reports %s but no matched size; refusing to invent a fill", post.OrderID, status)
	}

	// Never leave a resting GTC order behind after the reconciliation window.
	cancelError := ""
	if !isTerminalOrderStatus(status) {
		if err := l.clob.CancelOrder(ctx, post.OrderID); err != nil {
			cancelError = err.Error()
		}
		if matchedShares <= 0 {
			return nil, fmt.Errorf("order %s did not fill within %s and cancel result: %s", post.OrderID, l.pollTimeout, cancelError)
		}
	}

	if matchedShares <= 0 {
		return nil, fmt.Errorf("order %s ended with status %s and no fill", post.OrderID, status)
	}

	fillStatus := "PARTIAL_FILLED"
	if matchedShares >= effectiveShares-1e-9 {
		fillStatus = "FILLED"
	}

	// Use the limit price as a conservative fill price. Exact per-trade VWAP
	// requires trade reconciliation, which belongs to the next layer.
	averagePrice := request.LimitPrice
	if lastOrder != nil && lastOrder.Price.Float64() > 0 {
		averagePrice = lastOrder.Price.Float64()
	}

	notional := matchedShares * averagePrice
	fee := feeModel.FeeForLevel(averagePrice, matchedShares)
	cash := -(notional + fee)
	if side == "SELL" {
		cash = notional - fee
	}

	return &ExecutionFill{
		OrderID:      post.OrderID,
		Status:       fillStatus,
		FilledShares: matchedShares,
		AveragePrice: averagePrice,
		NotionalUSD:  notional,
		FeeUSD:       fee,
		CashUSD:      cash,
		Raw: map[string]interface{}{
			"exchange_status":   status,
			"order_type":        l.orderType,
			"fee_rate_bps":      feeRateBps,
			"fee_schedule_rate": feeModel.Rate,
			"fee_source":        "fee_schedule",
			"neg_risk":          negRisk,
			"order_hash":        order.OrderHash,
			"requested_shares":  request.Shares,
			"effective_shares":  effectiveShares,
			"max_cash_usd":      maxCash,
		},
	}, nil
}

// resolveFeeModel prefers an explicit request rate, then the market's
// feeSchedule. It fails closed instead of guessing from takerBaseFee.
func (l *LiveExecutor) resolveFeeModel(ctx context.Context, request OrderRequest) (FeeModel, error) {
	if request.FeeRateKnown || request.FeeRate > 0 {
		return FeeModel{Rate: request.FeeRate, Exponent: request.FeeExponent}, nil
	}
	if l.feeResolver == nil {
		return FeeModel{}, fmt.Errorf("fee schedule unavailable: set a FeeScheduleResolver or pass the market's feeSchedule rate; the legacy takerBaseFee is not the fee rate")
	}
	market, err := l.feeResolver.GetMarketByTokenID(ctx, request.TokenID)
	if err != nil {
		return FeeModel{}, fmt.Errorf("resolve fee schedule: %w", err)
	}
	if market == nil || !market.FeeRateKnown {
		return FeeModel{}, fmt.Errorf("market does not expose a feeSchedule for token %s; refusing to estimate fees from takerBaseFee", request.TokenID)
	}
	return FeeModel{Rate: market.FeeRate, Exponent: MarketFeeExponent(*market)}, nil
}

// maxSharesForCash returns the largest share count whose notional plus the
// protocol fee fits the cash cap.
func maxSharesForCash(cashUSD, price float64, fees FeeModel) float64 {
	if cashUSD <= 0 || price <= 0 || price >= 1 {
		return 0
	}
	costPerShare := price + fees.FeeForLevel(price, 1)
	if costPerShare <= 0 {
		return 0
	}
	return cashUSD / costPerShare
}

func isTerminalOrderStatus(status string) bool {
	switch strings.ToUpper(status) {
	case "MATCHED", "FILLED", "CANCELED", "CANCELLED", "EXPIRED":
		return true
	default:
		return false
	}
}

func isMatchedStatus(status string) bool {
	switch strings.ToUpper(status) {
	case "MATCHED", "FILLED":
		return true
	default:
		return false
	}
}

func parseResponseShares(side, takingAmount, makingAmount string) float64 {
	value := takingAmount
	if side == "SELL" {
		value = makingAmount
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	// CLOB responses use token decimals (1e6); tolerate decimal strings too.
	if parsed > 1e6 {
		parsed /= 1e6
	}
	return parsed
}
