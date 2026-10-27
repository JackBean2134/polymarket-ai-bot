package polymarket

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGammaMarketUnmarshalMatchesLivePayload(t *testing.T) {
	payload := `{
		"id":"559651",
		"question":"Xi Jinping out before 2027?",
		"slug":"xi-jinping-out-before-2027",
		"outcomes":"[\"Yes\", \"No\"]",
		"outcomePrices":"[\"0.0365\", \"0.9635\"]",
		"volume":"16177554.305146",
		"volume24hr":445354.605308,
		"liquidity":"268993.4484",
		"endDate":"2027-01-01T04:59:00Z",
		"clobTokenIds":"[\"111\", \"222\"]",
		"active":true,
		"closed":false,
		"acceptingOrders":true,
		"negRisk":true,
		"takerBaseFee":1000,
		"makerBaseFee":1000,
		"orderPriceMinTickSize":0.001,
		"orderMinSize":5,
		"events":[{"id":"30828"}]
	}`

	var market Market
	if err := json.Unmarshal([]byte(payload), &market); err != nil {
		t.Fatalf("unmarshal live payload: %v", err)
	}
	if market.Title != "Xi Jinping out before 2027?" || market.EventID != "30828" {
		t.Fatalf("unexpected title/event: %+v", market)
	}
	if market.YesPrice != 0.0365 || market.NoPrice != 0.9635 {
		t.Fatalf("prices were not parsed as decimals: %+v", market)
	}
	if market.Volume != 16177554.305146 || market.Volume24h != 445354.605308 || market.Liquidity != 268993.4484 {
		t.Fatalf("numeric strings were not parsed: %+v", market)
	}
	if !market.NegRisk || market.TakerBaseFee != 1000 || market.TickSize != 0.001 || market.MinOrderSize != 5 {
		t.Fatalf("live risk/fee fields were not parsed: %+v", market)
	}
	yes, no, err := market.BinaryTokenIDs()
	if err != nil || yes != "111" || no != "222" {
		t.Fatalf("unexpected token IDs: %q %q %v", yes, no, err)
	}
}

func TestOrderBookUnmarshalMatchesLivePayload(t *testing.T) {
	payload := `{
		"market":"0xabc",
		"asset_id":"111",
		"timestamp":"1793063863942",
		"hash":"deadbeef",
		"bids":[{"price":"0.30","size":"10"},{"price":"0.36","size":"5"}],
		"asks":[{"price":"0.40","size":"7"},{"price":"0.38","size":"3"}]
	}`

	var book OrderBook
	if err := json.Unmarshal([]byte(payload), &book); err != nil {
		t.Fatalf("unmarshal order book: %v", err)
	}
	if book.TokenID != "111" || book.ConditionID != "0xabc" {
		t.Fatalf("unexpected book identity: %+v", book)
	}
	if len(book.Bids) != 2 || book.Bids[0].Price != 0.36 {
		t.Fatalf("bids are not sorted descending: %+v", book.Bids)
	}
	if len(book.Asks) != 2 || book.Asks[0].Price != 0.38 {
		t.Fatalf("asks are not sorted ascending: %+v", book.Asks)
	}
	if book.Timestamp.IsZero() {
		t.Fatal("millisecond timestamp was not parsed")
	}
}

func TestCompareWithMarketUsesDecimalPrices(t *testing.T) {
	predictor := &AIPredictor{}
	prediction := &AIPrediction{MarketID: "m", Prediction: "YES", Probability: 0.65}

	assessment := predictor.CompareWithMarket(prediction, 0.50)
	if assessment.MarketImpliedProb != 0.50 {
		t.Fatalf("market price must stay decimal, got %f", assessment.MarketImpliedProb)
	}
	if assessment.Edge < 0.149 || assessment.Edge > 0.151 {
		t.Fatalf("unexpected edge: %f", assessment.Edge)
	}
	if assessment.Recommendation != "STRONG_BUY_YES" {
		t.Fatalf("unexpected recommendation: %s", assessment.Recommendation)
	}

	// A value written as 50 cents is invalid in the decimal contract and must
	// not be silently converted into a huge edge.
	invalid := predictor.CompareWithMarket(prediction, 50)
	if invalid.Recommendation != "HOLD" {
		t.Fatalf("non-decimal price should not produce a trade, got %s", invalid.Recommendation)
	}
}

func TestParsePredictionRequiresYesProbabilityAndDerivesLabel(t *testing.T) {
	predictor := &AIPredictor{}
	prediction, err := predictor.parsePrediction("```json\n{\"prediction\":\"NO\",\"yes_probability\":0.70,\"confidence\":0.8,\"reasoning\":\"x\"}\n```", &PredictionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if prediction.Probability != 0.70 || prediction.Prediction != "YES" {
		t.Fatalf("yes_probability must be authoritative: %+v", prediction)
	}
	if !strings.Contains(prediction.Reasoning, "overridden") {
		t.Fatalf("inconsistent model label should be noted: %s", prediction.Reasoning)
	}

	if _, err := predictor.parsePrediction(`{"prediction":"YES","confidence":0.8}`, &PredictionRequest{}); err == nil {
		t.Fatal("missing probability must fail instead of fabricating a 60% forecast")
	}
}

func TestApproveOrderCapsAndSettlementReleasesCapital(t *testing.T) {
	riskManager := NewRiskManager(&RiskConfig{TotalCapital: 10000, MaxPositionSize: 500, MaxPositionPercent: 0.05})
	signal := &TradingSignal{
		MarketID:   "m",
		Confidence: 80,
		Assessment: &ValueAssessment{AIProbability: 0.70, MarketImpliedProb: 0.50, Edge: 0.20},
	}

	approved, err := riskManager.ApproveOrder(signal, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if approved != 500 {
		t.Fatalf("expected the $500 max position cap, got %f", approved)
	}

	riskManager.RecordTrade(&TradeExecution{MarketID: "m", Size: approved, Price: 0.5, Timestamp: time.Now()})
	if metrics := riskManager.GetRiskMetrics(); metrics.UsedCapital != 500 {
		t.Fatalf("expected 500 used capital, got %f", metrics.UsedCapital)
	}

	riskManager.RecordSettlement("m", 500, 120)
	metrics := riskManager.GetRiskMetrics()
	if metrics.UsedCapital != 0 || metrics.TotalPnL != 120 || metrics.PositionCount != 0 {
		t.Fatalf("settlement did not release capital: %+v", metrics)
	}
}

type staticBookProvider struct {
	book *OrderBook
}

func (s staticBookProvider) GetOrderBook(ctx context.Context, tokenID string) (*OrderBook, error) {
	return s.book, nil
}

func TestProcessSignalEnforcesApprovedSizeInPaperMode(t *testing.T) {
	client := NewClient(ClientConfig{})
	analyzer := NewMarketAnalyzer(client)
	riskManager := NewRiskManager(&RiskConfig{TotalCapital: 10000, MaxPositionSize: 500, MaxPositionPercent: 0.05})
	engine := NewStrategyEngine(client, analyzer, nil, riskManager)
	engine.SetExecutor(NewPaperExecutor(staticBookProvider{
		book: &OrderBook{Asks: []Order{{Price: 0.50, Size: 5000}}},
	}, 0))

	signal := &TradingSignal{
		MarketID:     "m",
		Strategy:     "VALUE_BET",
		Confidence:   80,
		CurrentPrice: 0.50,
		YesTokenID:   "yes",
		NoTokenID:    "no",
		Timestamp:    time.Now(),
		Assessment: &ValueAssessment{
			AIProbability:     0.70,
			MarketImpliedProb: 0.50,
			Edge:              0.20,
			EdgePercent:       40,
		},
	}

	execution, err := engine.ProcessSignal(context.Background(), signal)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != "PAPER_FILLED" || execution.Size > 500 || execution.Size > signal.ApprovedSize+1e-9 {
		t.Fatalf("unexpected paper execution: %+v (approved %f)", execution, signal.ApprovedSize)
	}
	if execution.Shares <= 0 || execution.Price != 0.50 {
		t.Fatalf("unexpected fill details: %+v", execution)
	}

	if err := engine.UpdatePositionPrice("m", "YES", 0.60); err != nil {
		t.Fatal(err)
	}
	if err := engine.ClosePosition(context.Background(), "m", "YES"); err != nil {
		t.Fatal(err)
	}
	metrics := riskManager.GetRiskMetrics()
	if metrics.UsedCapital != 0 {
		t.Fatalf("closing a paper position must release capital, got %+v", metrics)
	}
}
