package polymarket

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

type fakeFeeResolver struct {
	market *Market
	err    error
	calls  int
}

func (f *fakeFeeResolver) GetMarketByTokenID(ctx context.Context, tokenID string) (*Market, error) {
	f.calls++
	return f.market, f.err
}

func TestLiveExecutorResolveFeeModel(t *testing.T) {
	exec := &LiveExecutor{}

	// An explicit request rate always wins and skips the resolver.
	model, err := exec.resolveFeeModel(context.Background(), OrderRequest{FeeRateKnown: true, FeeRate: 0, FeeExponent: 1})
	if err != nil {
		t.Fatalf("explicit zero rate must be accepted: %v", err)
	}
	if model.Rate != 0 || model.Exponent != 1 {
		t.Fatalf("unexpected model: %+v", model)
	}

	// Without a resolver it must fail closed, never guess from takerBaseFee.
	if _, err := exec.resolveFeeModel(context.Background(), OrderRequest{}); err == nil || !strings.Contains(err.Error(), "fee schedule unavailable") {
		t.Fatalf("expected a fail-closed error, got %v", err)
	}

	resolverErr := &fakeFeeResolver{err: errors.New("gamma down")}
	exec = &LiveExecutor{feeResolver: resolverErr}
	if _, err := exec.resolveFeeModel(context.Background(), OrderRequest{TokenID: "t1"}); err == nil || !strings.Contains(err.Error(), "gamma down") {
		t.Fatalf("expected the resolver error to surface, got %v", err)
	}

	unknown := &fakeFeeResolver{market: &Market{FeeRateKnown: false, TakerBaseFee: 1000}}
	exec = &LiveExecutor{feeResolver: unknown}
	if _, err := exec.resolveFeeModel(context.Background(), OrderRequest{TokenID: "t1"}); err == nil || !strings.Contains(err.Error(), "feeSchedule") {
		t.Fatalf("expected a refusal without a schedule, got %v", err)
	}

	free := &fakeFeeResolver{market: &Market{FeeRateKnown: true, FeeRate: 0}}
	exec = &LiveExecutor{feeResolver: free}
	model, err = exec.resolveFeeModel(context.Background(), OrderRequest{TokenID: "t1"})
	if err != nil {
		t.Fatalf("a fee-free market must resolve: %v", err)
	}
	if model.Rate != 0 {
		t.Fatalf("expected a zero rate, got %f", model.Rate)
	}

	sports := &fakeFeeResolver{market: &Market{FeeRateKnown: true, FeeRate: 0.05, FeeExponent: 1}}
	exec = &LiveExecutor{feeResolver: sports}
	model, err = exec.resolveFeeModel(context.Background(), OrderRequest{TokenID: "t1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if math.Abs(model.Rate-0.05) > 1e-9 || model.Exponent != 1 {
		t.Fatalf("unexpected model: %+v", model)
	}
	if sports.calls != 1 {
		t.Fatalf("expected one resolver call, got %d", sports.calls)
	}
}

func TestMaxSharesForCashUsesProtocolFee(t *testing.T) {
	// 25 / (0.50 + 0.05×0.5×0.5) = 25 / 0.5125 = 48.7805
	shares := maxSharesForCash(25, 0.50, FeeModel{Rate: 0.05, Exponent: 1})
	if math.Abs(shares-48.7804878) > 1e-6 {
		t.Fatalf("unexpected share cap: %f", shares)
	}
	if got := maxSharesForCash(25, 0.50, FeeModel{}); got != 50 {
		t.Fatalf("fee-free cap should be 50, got %f", got)
	}
	if got := maxSharesForCash(0, 0.5, FeeModel{}); got != 0 {
		t.Fatalf("zero cash must cap at zero, got %f", got)
	}
	if got := maxSharesForCash(25, 1.0, FeeModel{}); got != 0 {
		t.Fatalf("invalid price must cap at zero, got %f", got)
	}
}

func TestMaxSharesForCashKeepsTotalWithinBudget(t *testing.T) {
	const cash = 25.0
	price := 0.37
	model := FeeModel{Rate: 0.07, Exponent: 1}
	shares := maxSharesForCash(cash, price, model)
	total := shares * (price + model.FeeForLevel(price, 1))
	if total > cash+1e-9 {
		t.Fatalf("total %.6f exceeds cap %.2f", total, cash)
	}
	if cash-total > 1e-9 {
		t.Fatalf("cap is not tight: total %.6f vs %.2f", total, cash)
	}
}
