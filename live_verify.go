package polymarket

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// Verify check statuses. A report is "ready" only when there are no fails and
// both the configuration and the market pass.
const (
	VerifyPass = "pass"
	VerifyWarn = "warn"
	VerifyFail = "fail"
	VerifySkip = "skip"
)

// VerifyCheck is one line of the pre-flight report.
type VerifyCheck struct {
	Group  string `json:"group"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// LiveVerifyReport is the output of -live-verify. It never sends an order.
type LiveVerifyReport struct {
	TokenID     string        `json:"token_id"`
	GeneratedAt time.Time     `json:"generated_at"`
	Side        string        `json:"side"`
	Shares      float64       `json:"shares"`
	LimitPrice  float64       `json:"limit_price"`
	MaxUSD      float64       `json:"max_usd"`
	Checks      []VerifyCheck `json:"checks"`
	Blockers    []string      `json:"blockers,omitempty"`
	Warnings    []string      `json:"warnings,omitempty"`
	ConfigReady bool          `json:"config_ready"`
	MarketReady bool          `json:"market_ready"`
	Ready       bool          `json:"ready_for_small_live_test"`
	Note        string        `json:"note"`
}

// AllowanceChecker reads on-chain approvals. Returning an error is treated as a
// warning by the verifier, not a blocker, because it may be an RPC outage.
type AllowanceChecker func(ctx context.Context, chainID int64, owner, exchange string, requiredUSD float64) (*AllowanceResult, error)

// LiveVerifyDeps are the collaborators the verifier may use. Signer is optional:
// without it the signing check is skipped instead of failing.
type LiveVerifyDeps struct {
	Config     *Config
	Client     *Client
	CLOB       *CLOBService
	Gamma      *GammaService
	Signer     *OrderSigner
	EnvLive    bool
	Allowances AllowanceChecker
}

// VerifyLiveOrder runs read-only pre-flight checks. It builds and hashes a
// signed order but never posts it.
func VerifyLiveOrder(ctx context.Context, deps LiveVerifyDeps, request OrderRequest) (*LiveVerifyReport, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("config is required")
	}
	if deps.CLOB == nil || deps.Gamma == nil {
		return nil, fmt.Errorf("CLOB and Gamma services are required")
	}
	tokenID := strings.TrimSpace(request.TokenID)
	if tokenID == "" {
		return nil, fmt.Errorf("token ID is required")
	}
	side := strings.ToUpper(strings.TrimSpace(request.Side))
	if side == "" {
		side = "BUY"
	}

	report := &LiveVerifyReport{
		TokenID:     tokenID,
		GeneratedAt: time.Now().UTC(),
		Side:        side,
		Shares:      request.Shares,
		LimitPrice:  request.LimitPrice,
		MaxUSD:      request.MaxCashUSD,
		Checks:      make([]VerifyCheck, 0, 16),
		Note:        "read-only pre-flight: no order is signed for submission and nothing is sent",
	}
	add := func(group, name, status, detail string) {
		report.Checks = append(report.Checks, VerifyCheck{Group: group, Name: name, Status: status, Detail: detail})
	}

	// --- configuration and identity -------------------------------------
	for _, check := range verifyConfigGates(deps.Config, deps.EnvLive, tokenID) {
		add(check.Group, check.Name, check.Status, check.Detail)
	}
	l2 := verifyL2Credentials(deps.Client)
	add(l2.Group, l2.Name, l2.Status, l2.Detail)
	if deps.Signer != nil {
		for _, check := range verifyIdentity(deps.Config, deps.Client, deps.Signer) {
			add(check.Group, check.Name, check.Status, check.Detail)
		}
	} else {
		add("config", "credentials.private_key", VerifySkip, "POLYMARKET_PRIVATE_KEY is not set; signing checks are skipped")
		add("config", "signer.poly_address", VerifySkip, "no signer available")
		add("config", "wallet.model", VerifySkip, "no signer available")
	}

	// --- market metadata ------------------------------------------------
	market, marketErr := deps.Gamma.GetMarketByTokenID(ctx, tokenID)
	if marketErr != nil {
		add("market", "market.exists", VerifyFail, marketErr.Error())
		report.finish()
		return report, nil
	}
	add("market", "market.exists", VerifyPass, fmt.Sprintf("market %s: %s", market.ID, strings.TrimSpace(market.Question)))

	verifyMarket(ctx, report, market, request, side, deps)
	report.finish()
	return report, nil
}

func (r *LiveVerifyReport) record(group, name, status, detail string) {
	r.Checks = append(r.Checks, VerifyCheck{Group: group, Name: name, Status: status, Detail: detail})
}

func verifyConfigGates(config *Config, envLive bool, tokenID string) []VerifyCheck {
	checks := make([]VerifyCheck, 0, 6)
	add := func(name, status, detail string) {
		checks = append(checks, VerifyCheck{Group: "config", Name: name, Status: status, Detail: detail})
	}

	if err := CheckStopFile(config.Trading.StopFile); err != nil {
		add("stop_file", VerifyFail, err.Error())
	} else {
		add("stop_file", VerifyPass, fmt.Sprintf("no kill switch at %q", config.Trading.StopFile))
	}

	if config.Trading.Mode == "live" && config.Trading.Enabled {
		add("live_switch", VerifyPass, "trading.mode=live and trading.enabled=true")
	} else {
		add("live_switch", VerifyWarn, fmt.Sprintf("mode=%q enabled=%v; live sending is disabled", config.Trading.Mode, config.Trading.Enabled))
	}
	if config.Trading.AllowLive {
		add("allow_live_config", VerifyPass, "trading.allow_live=true")
	} else {
		add("allow_live_config", VerifyWarn, "trading.allow_live=false")
	}
	if envLive {
		add("allow_live_env", VerifyPass, "POLYMARKET_ALLOW_LIVE=1")
	} else {
		add("allow_live_env", VerifyWarn, "POLYMARKET_ALLOW_LIVE is not 1/true")
	}
	switch {
	case len(config.Trading.AllowedTokens) == 0:
		add("token_whitelist", VerifyWarn, "trading.allowed_tokens is empty; live orders are refused")
	case containsToken(config.Trading.AllowedTokens, tokenID):
		add("token_whitelist", VerifyPass, "token is in the live whitelist")
	default:
		add("token_whitelist", VerifyFail, "token is not in trading.allowed_tokens")
	}
	return checks
}

func verifyL2Credentials(client *Client) VerifyCheck {
	if client != nil && client.HasL2Credentials() {
		return VerifyCheck{Group: "config", Name: "credentials.l2", Status: VerifyPass,
			Detail: "API key, secret and wallet address are present"}
	}
	return VerifyCheck{Group: "config", Name: "credentials.l2", Status: VerifyFail,
		Detail: "missing POLYMARKET_API_KEY / API_SECRET / WALLET_ADDRESS"}
}

func verifyIdentity(config *Config, client *Client, signer *OrderSigner) []VerifyCheck {
	checks := make([]VerifyCheck, 0, 4)
	add := func(name, status, detail string) {
		checks = append(checks, VerifyCheck{Group: "config", Name: name, Status: status, Detail: detail})
	}

	add("credentials.private_key", VerifyPass, "POLYMARKET_PRIVATE_KEY is set")

	signerAddress := signer.Address()
	polyAddress := strings.TrimSpace(config.Polymarket.WalletAddress)
	switch {
	case polyAddress == "":
		add("signer.poly_address", VerifyWarn, "POLYMARKET_WALLET_ADDRESS is empty")
	case strings.EqualFold(polyAddress, signerAddress):
		add("signer.poly_address", VerifyPass, "wallet address matches the signer")
	default:
		add("signer.poly_address", VerifyFail, fmt.Sprintf("wallet %s does not match signer %s", polyAddress, signerAddress))
	}

	funder := strings.TrimSpace(config.Polymarket.FunderAddress)
	switch config.Polymarket.SignatureType {
	case 0:
		effective := funder
		if effective == "" {
			effective = strings.TrimSpace(signer.Funder())
		}
		if effective == "" || strings.EqualFold(effective, signerAddress) {
			add("wallet.model", VerifyPass, "EOA signature type: funder is empty or equals the signer")
		} else {
			add("wallet.model", VerifyFail, "signature_type=0 requires the funder to be empty or the signer")
		}
	case 1, 2:
		if funder == "" {
			add("wallet.model", VerifyFail, "signature_type 1/2 requires POLYMARKET_FUNDER_ADDRESS")
		} else if strings.EqualFold(funder, signerAddress) {
			add("wallet.model", VerifyWarn, fmt.Sprintf("funder equals the EOA signer (%s); confirm this is really a proxy/safe wallet", funder))
		} else {
			add("wallet.model", VerifyPass, fmt.Sprintf("proxy/safe funder %s", funder))
		}
	default:
		add("wallet.model", VerifyFail, fmt.Sprintf("unsupported signature_type %d", config.Polymarket.SignatureType))
	}
	return checks
}

func verifyMarket(ctx context.Context, report *LiveVerifyReport, market *Market, request OrderRequest, side string, deps LiveVerifyDeps) {
	tradable := !market.Closed && market.AcceptingOrders && market.Active
	status := VerifyPass
	if !tradable {
		status = VerifyFail
	}
	report.record("market", "market.tradable", status,
		fmt.Sprintf("closed=%v active=%v acceptingOrders=%v", market.Closed, market.Active, market.AcceptingOrders))

	minOrder := market.MinOrderSize
	if minOrder < 5 {
		minOrder = 5
	}
	if request.Shares < minOrder {
		report.record("market", "market.min_order", VerifyFail,
			fmt.Sprintf("%.0f shares is below the %.0f share minimum", request.Shares, minOrder))
	} else {
		report.record("market", "market.min_order", VerifyPass,
			fmt.Sprintf("%.0f shares >= %.0f share minimum", request.Shares, minOrder))
	}

	tickSize := market.TickSize
	if tickSize <= 0 {
		tickSize = 0.001
	}

	book, bookErr := deps.CLOB.GetOrderBook(ctx, request.TokenID)
	price := request.LimitPrice
	if bookErr == nil && price <= 0 {
		if side == "SELL" {
			if best, _, ok := bestBid(book); ok {
				price = best
			}
		} else if best, _, ok := bestAsk(book); ok {
			price = best
		}
		report.LimitPrice = price
	}

	if price <= 0 || price >= 1 {
		report.record("market", "market.tick", VerifyFail, "no valid reference price: pass -live-price or provide book liquidity")
	} else if math.Abs(math.Round(price/tickSize)*tickSize-price) < 1e-9 {
		report.record("market", "market.tick", VerifyPass, fmt.Sprintf("price %.4f is aligned to tick %.4f", price, tickSize))
	} else {
		report.record("market", "market.tick", VerifyFail, fmt.Sprintf("price %.6f is not aligned to tick %.4f", price, tickSize))
	}

	report.Checks = append(report.Checks, feeCheck(market, request.Shares, price))
	report.Checks = append(report.Checks, feeLegacyCheck(market, request.Shares, price))

	if bookErr != nil {
		report.record("market", "book.depth", VerifyFail, fmt.Sprintf("order book unavailable: %v", bookErr))
	} else {
		available := cumulativeSizeAtLimit(book, side, price)
		switch {
		case price <= 0 || available <= 0:
			report.record("market", "book.depth", VerifyFail, fmt.Sprintf("no %s liquidity at %.4f", strings.ToLower(side), price))
		case available < request.Shares:
			report.record("market", "book.depth", VerifyFail, fmt.Sprintf("only %.1f shares available at %.4f, need %.0f", available, price, request.Shares))
		case available < request.Shares*2:
			report.record("market", "book.depth", VerifyWarn, fmt.Sprintf("%.1f shares available at %.4f; thin for %.0f requested", available, price, request.Shares))
		default:
			report.record("market", "book.depth", VerifyPass, fmt.Sprintf("%.1f shares available at %.4f", available, price))
		}
	}

	if deps.Signer == nil {
		report.record("order", "order.signable", VerifySkip, "no signer available")
	} else if price <= 0 || price >= 1 {
		report.record("order", "order.signable", VerifySkip, "no valid limit price to sign")
	} else if signed, signErr := deps.Signer.BuildSignedOrder(request.TokenID, side, request.Shares, price, tickSize, market.TakerBaseFee, market.NegRisk); signErr != nil {
		report.record("order", "order.signable", VerifyFail, signErr.Error())
	} else {
		report.record("order", "order.signable", VerifyPass, fmt.Sprintf(
			"signed dry-run ok: hash %s, makerAmount %s, takerAmount %s", signed.OrderHash, signed.MakerAmount, signed.TakerAmount))
	}

	verifyAllowance(ctx, report, market, request, price, deps)
}

func verifyAllowance(ctx context.Context, report *LiveVerifyReport, market *Market, request OrderRequest, price float64, deps LiveVerifyDeps) {
	if deps.Allowances == nil {
		report.record("config", "allowance.usdc", VerifySkip,
			"no RPC configured; pass -polygon-rpc or set POLYGON_RPC_URL to verify USDC/CTF approvals")
		return
	}
	chainID := deps.Config.Polymarket.ChainID
	contract, ok := contractsByChain[chainID]
	if !ok {
		report.record("config", "allowance.usdc", VerifySkip, fmt.Sprintf("no exchange contracts known for chain %d", chainID))
		return
	}
	exchange := contract.exchange
	if market.NegRisk {
		exchange = contract.negRisk
	}

	owner := strings.TrimSpace(deps.Config.Polymarket.FunderAddress)
	if owner == "" {
		owner = strings.TrimSpace(deps.Config.Polymarket.WalletAddress)
	}
	if owner == "" && deps.Signer != nil {
		owner = deps.Signer.Address()
	}
	if owner == "" {
		report.record("config", "allowance.usdc", VerifySkip, "no funder or wallet address to check")
		return
	}

	required := request.Shares * price
	if market.FeeRateKnown {
		required += TakerFeeUSD(price, request.Shares, market.FeeRate, MarketFeeExponent(*market))
	}

	result, err := deps.Allowances(ctx, chainID, owner, exchange, required)
	if err != nil {
		report.record("config", "allowance.usdc", VerifyWarn, fmt.Sprintf("could not read allowances: %v", err))
		return
	}
	if result.USDCSufficient {
		report.record("config", "allowance.usdc", VerifyPass,
			fmt.Sprintf("USDC allowance %.6f >= required %.6f for %s", result.USDCAllowance, result.RequiredUSD, result.Exchange))
	} else {
		report.record("config", "allowance.usdc", VerifyFail,
			fmt.Sprintf("USDC allowance %.6f < required %.6f for %s; approve USDC to the exchange contract", result.USDCAllowance, result.RequiredUSD, result.Exchange))
	}
	if result.BalanceEnough {
		report.record("config", "balance.usdc", VerifyPass,
			fmt.Sprintf("USDC balance %.6f >= required %.6f", result.USDCBalance, result.RequiredUSD))
	} else {
		report.record("config", "balance.usdc", VerifyFail,
			fmt.Sprintf("USDC balance %.6f < required %.6f in %s; fund this wallet before trading", result.USDCBalance, result.RequiredUSD, owner))
	}
	if result.CTFApproved {
		report.record("config", "allowance.ctf", VerifyPass, "CTF isApprovedForAll(exchange) = true")
	} else {
		report.record("config", "allowance.ctf", VerifyWarn,
			"CTF isApprovedForAll(exchange) = false; a BUY can still settle but selling or merging needs setApprovalForAll")
	}
}

func feeCheck(market *Market, shares, price float64) VerifyCheck {
	if !market.FeeRateKnown {
		return VerifyCheck{Group: "market", Name: "market.fees", Status: VerifyFail,
			Detail: "no feeSchedule published; live execution refuses to size this order"}
	}
	exponent := MarketFeeExponent(*market)
	fee := TakerFeeUSD(price, shares, market.FeeRate, exponent)
	return VerifyCheck{Group: "market", Name: "market.fees", Status: VerifyPass,
		Detail: fmt.Sprintf("rate %.4f exponent %.0f gives %.4f USDC for %.0f shares at %.4f", market.FeeRate, exponent, fee, shares, price)}
}

func feeLegacyCheck(market *Market, shares, price float64) VerifyCheck {
	legacy := float64(market.TakerBaseFee) / 10000 * price * shares
	if market.FeeRateKnown && market.TakerBaseFee > 0 {
		real := TakerFeeUSD(price, shares, market.FeeRate, MarketFeeExponent(*market))
		if legacy > real*1.5+1e-9 {
			return VerifyCheck{Group: "market", Name: "market.fee_legacy", Status: VerifyWarn,
				Detail: fmt.Sprintf("legacy takerBaseFee %d would charge %.4f vs %.4f from feeSchedule; never size from the legacy field", market.TakerBaseFee, legacy, real)}
		}
	}
	return VerifyCheck{Group: "market", Name: "market.fee_legacy", Status: VerifyPass,
		Detail: fmt.Sprintf("legacy takerBaseFee %d is not used for sizing", market.TakerBaseFee)}
}

func (r *LiveVerifyReport) finish() {
	configOK := true
	marketOK := true
	for _, check := range r.Checks {
		switch check.Status {
		case VerifyFail:
			r.Blockers = append(r.Blockers, fmt.Sprintf("%s: %s", check.Name, check.Detail))
			if check.Group == "config" {
				configOK = false
			} else {
				marketOK = false
			}
		case VerifyWarn:
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: %s", check.Name, check.Detail))
			if check.Group == "config" {
				configOK = false
			}
		}
	}
	r.ConfigReady = configOK
	r.MarketReady = marketOK
	r.Ready = configOK && marketOK && len(r.Blockers) == 0
}

func bestBid(book *OrderBook) (price, size float64, ok bool) {
	if book == nil {
		return 0, 0, false
	}
	for _, level := range book.Bids {
		if level.Price <= 0 || level.Price >= 1 || level.Size <= 0 {
			continue
		}
		if !ok || level.Price > price {
			price = level.Price
			size = level.Size
			ok = true
		}
	}
	return price, size, ok
}

// cumulativeSizeAtLimit sums the visible size a market order could take at or
// better than the limit price.
func cumulativeSizeAtLimit(book *OrderBook, side string, limitPrice float64) float64 {
	if book == nil || limitPrice <= 0 || limitPrice >= 1 {
		return 0
	}
	sell := strings.EqualFold(side, "SELL")
	levels := book.Asks
	if sell {
		levels = book.Bids
	}
	total := 0.0
	for _, level := range levels {
		if level.Price <= 0 || level.Price >= 1 || level.Size <= 0 {
			continue
		}
		if sell {
			if level.Price < limitPrice-1e-9 {
				continue
			}
		} else if level.Price > limitPrice+1e-9 {
			continue
		}
		total += level.Size
	}
	return total
}
