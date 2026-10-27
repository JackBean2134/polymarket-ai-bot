package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type AIPredictor struct {
	llmClient *LLMClient
	cache     *PredictionCache
}

type PredictionCache struct {
	mu          sync.RWMutex
	predictions map[string]*AIPrediction
	lastUpdate  map[string]time.Time
	ttl         time.Duration
}

type AIPrediction struct {
	MarketID             string                `json:"market_id"`
	MarketTitle          string                `json:"market_title"`
	Prediction           string                `json:"prediction"`
	Probability          float64               `json:"probability"`
	Confidence           float64               `json:"confidence"`
	Reasoning            string                `json:"reasoning"`
	KeyFactors           []string              `json:"key_factors"`
	RiskAssessment       string                `json:"risk_assessment"`
	TimeHorizon          string                `json:"time_horizon"`
	AlternativeScenarios []AlternativeScenario `json:"alternative_scenarios"`
	CreatedAt            time.Time             `json:"created_at"`
	ExpiresAt            time.Time             `json:"expires_at"`
}

type AlternativeScenario struct {
	Scenario    string  `json:"scenario"`
	Probability float64 `json:"probability"`
	Impact      string  `json:"impact"`
}

type PredictionRequest struct {
	MarketID          string             `json:"market_id"`
	MarketTitle       string             `json:"market_title"`
	MarketDescription string             `json:"market_description,omitempty"`
	MarketData        *MarketContext     `json:"market_data"`
	HistoricalData    *HistoricalContext `json:"historical_data,omitempty"`
	ExternalFactors   []ExternalFactor   `json:"external_factors,omitempty"`
}

type MarketContext struct {
	CurrentPrice       float64 `json:"current_price"`
	YesPrice           float64 `json:"yes_price"`
	NoPrice            float64 `json:"no_price"`
	Volume24h          float64 `json:"volume_24h"`
	OpenInterest       float64 `json:"open_interest"`
	Liquidity          float64 `json:"liquidity"`
	TimeToExpiry       string  `json:"time_to_expiry"`
	ExpirationValue    string  `json:"expiration_value"`
	OrderBookImbalance float64 `json:"order_book_imbalance"`
	RecentTrend        string  `json:"recent_trend"`
}

type HistoricalContext struct {
	PriceHistory  []PricePoint   `json:"price_history"`
	VolumeHistory []VolumePoint  `json:"volume_history"`
	SimilarEvents []SimilarEvent `json:"similar_events,omitempty"`
}

type PricePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Price     float64   `json:"price"`
}

type VolumePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Volume    float64   `json:"volume"`
}

type SimilarEvent struct {
	Title    string  `json:"title"`
	Result   string  `json:"result"`
	Accuracy float64 `json:"accuracy"`
}

type ExternalFactor struct {
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Impact      string    `json:"impact"`
	Confidence  float64   `json:"confidence"`
	Source      string    `json:"source,omitempty"`
	Timestamp   time.Time `json:"timestamp,omitempty"`
}

type PredictionResponse struct {
	Success    bool          `json:"success"`
	Prediction *AIPrediction `json:"prediction,omitempty"`
	Error      string        `json:"error,omitempty"`
	Latency    int64         `json:"latency_ms"`
	TokenUsage *TokenUsage   `json:"token_usage,omitempty"`
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func NewAIPredictor(llmClient *LLMClient) *AIPredictor {
	return NewAIPredictorWithTTL(llmClient, 30*time.Minute)
}

func NewAIPredictorWithTTL(llmClient *LLMClient, ttl time.Duration) *AIPredictor {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &AIPredictor{
		llmClient: llmClient,
		cache: &PredictionCache{
			predictions: make(map[string]*AIPrediction),
			lastUpdate:  make(map[string]time.Time),
			ttl:         ttl,
		},
	}
}

func (ap *AIPredictor) Predict(ctx context.Context, req *PredictionRequest) (*AIPrediction, error) {
	if req == nil {
		return nil, fmt.Errorf("prediction request is nil")
	}
	if ap.llmClient == nil {
		return nil, fmt.Errorf("LLM client is not configured")
	}
	cacheKey := req.MarketID
	if cached, ok := ap.cache.get(cacheKey); ok {
		return cached, nil
	}

	prompt := ap.buildPrompt(req)

	response, err := ap.llmClient.Chat(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM prediction failed: %w", err)
	}

	prediction, err := ap.parsePrediction(response, req)
	if err != nil {
		return nil, fmt.Errorf("failed to parse prediction: %w", err)
	}

	prediction.MarketID = req.MarketID
	prediction.MarketTitle = req.MarketTitle
	prediction.CreatedAt = time.Now()
	prediction.ExpiresAt = time.Now().Add(ap.cache.ttl)

	ap.cache.set(cacheKey, prediction)

	return prediction, nil
}

func (ap *AIPredictor) buildPrompt(req *PredictionRequest) string {
	var sb strings.Builder

	sb.WriteString("你是一位专业的预测市场分析专家。请基于以下数据对 Polymarket 市场进行概率评估和预测。\n\n")

	sb.WriteString(fmt.Sprintf("## 市场信息\n"))
	sb.WriteString(fmt.Sprintf("市场标题：%s\n", req.MarketTitle))
	sb.WriteString(fmt.Sprintf("市场 ID: %s\n", req.MarketID))
	if req.MarketDescription != "" {
		sb.WriteString(fmt.Sprintf("市场规则/描述：%s\n", req.MarketDescription))
	}
	sb.WriteString("\n")

	if req.MarketData != nil {
		sb.WriteString("## 当前市场数据（价格均为 0-1 的概率）\n")
		sb.WriteString(fmt.Sprintf("当前 YES 价格：%.4f（隐含概率 %.2f%%）\n", req.MarketData.CurrentPrice, req.MarketData.CurrentPrice*100))
		sb.WriteString(fmt.Sprintf("YES 买价：%.4f；YES 卖价：%.4f\n", req.MarketData.YesPrice, req.MarketData.NoPrice))
		sb.WriteString(fmt.Sprintf("24 小时交易量：$%.2f\n", req.MarketData.Volume24h))
		sb.WriteString(fmt.Sprintf("持仓量：$%.2f\n", req.MarketData.OpenInterest))
		sb.WriteString(fmt.Sprintf("流动性：$%.2f\n", req.MarketData.Liquidity))
		sb.WriteString(fmt.Sprintf("到期时间：%s\n", req.MarketData.TimeToExpiry))
		sb.WriteString(fmt.Sprintf("订单簿不平衡度：%.2f%%\n", req.MarketData.OrderBookImbalance))
		sb.WriteString(fmt.Sprintf("近期趋势：%s\n\n", req.MarketData.RecentTrend))
	}

	if req.HistoricalData != nil && len(req.HistoricalData.PriceHistory) > 0 {
		sb.WriteString("## 历史价格数据（0-1 概率）\n")
		for i, point := range req.HistoricalData.PriceHistory {
			if i >= 10 {
				break
			}
			sb.WriteString(fmt.Sprintf("- %s: %.4f\n", point.Timestamp.Format("2006-01-02"), point.Price))
		}
		sb.WriteString("\n")
	}

	if len(req.ExternalFactors) > 0 {
		sb.WriteString("## 外部因素\n")
		for _, factor := range req.ExternalFactors {
			sb.WriteString(fmt.Sprintf("- [%s] %s (影响：%s, 置信度：%.2f%%)\n",
				factor.Category, factor.Description, factor.Impact, factor.Confidence*100))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 分析要求\n\n")
	sb.WriteString("1. 先给出该事件的基准概率（base rate），再结合当前信息更新。\n")
	sb.WriteString("2. 不要把当前市场价格直接当作真值；市场价只是一个可比较的锚点。\n")
	sb.WriteString("3. 明确说明哪些信息是你不知道的，避免编造新闻或数据。\n")
	sb.WriteString("4. 给出 P(YES)，即 YES 结果发生的概率（0-1 小数）。\n")
	sb.WriteString("5. 给出置信度（0-1 小数），以及到期前可能改变结论的关键事件。\n")
	sb.WriteString("6. 如果信息不足，yes_probability 应接近市场隐含概率，confidence 应较低。\n\n")

	sb.WriteString("## 输出格式\n")
	sb.WriteString("只输出一个 JSON 对象，不要包含 markdown 代码块标记：\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"prediction\": \"YES 或 NO\",\n")
	sb.WriteString("  \"yes_probability\": 0.00,\n")
	sb.WriteString("  \"confidence\": 0.00,\n")
	sb.WriteString("  \"reasoning\": \"详细推理\",\n")
	sb.WriteString("  \"key_factors\": [\"因素 1\", \"因素 2\", \"因素 3\"],\n")
	sb.WriteString("  \"risk_assessment\": \"风险评估\",\n")
	sb.WriteString("  \"time_horizon\": \"时间范围\",\n")
	sb.WriteString("  \"alternative_scenarios\": [\n")
	sb.WriteString("    {\"scenario\": \"情景描述\", \"probability\": 0.00, \"impact\": \"影响程度\"}\n")
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")

	return sb.String()
}

func (ap *AIPredictor) parsePrediction(response string, req *PredictionRequest) (*AIPrediction, error) {
	response = strings.TrimSpace(response)
	if start := strings.Index(response, "{"); start >= 0 {
		if end := strings.LastIndex(response, "}"); end > start {
			response = response[start : end+1]
		}
	}

	var result struct {
		Prediction           string                `json:"prediction"`
		Probability          *float64              `json:"probability"`
		YesProbability       *float64              `json:"yes_probability"`
		Confidence           float64               `json:"confidence"`
		Reasoning            string                `json:"reasoning"`
		KeyFactors           []string              `json:"key_factors"`
		RiskAssessment       string                `json:"risk_assessment"`
		TimeHorizon          string                `json:"time_horizon"`
		AlternativeScenarios []AlternativeScenario `json:"alternative_scenarios"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("model response is not valid JSON: %w", err)
	}

	var probability float64
	switch {
	case result.YesProbability != nil:
		probability = *result.YesProbability
	case result.Probability != nil:
		probability = *result.Probability
	default:
		return nil, fmt.Errorf("model response is missing yes_probability")
	}

	// Accept percentages for compatibility, then enforce [0,1].
	if probability < 0 || probability > 1 {
		probability /= 100
	}
	if probability < 0 || probability > 1 {
		return nil, fmt.Errorf("yes_probability %v is outside [0,1]", probability)
	}
	probability = clampProbability(probability)

	confidence := result.Confidence
	if confidence < 0 || confidence > 1 {
		confidence /= 100
	}
	if confidence < 0 || confidence > 1 {
		confidence = 0
	}

	// The numeric P(YES) is authoritative because it is what sizing and edge
	// calculations consume. The textual prediction is derived from it to avoid
	// inverted bets when an LLM labels probabilities inconsistently.
	prediction := "YES"
	if probability < 0.5 {
		prediction = "NO"
	}

	reasoning := result.Reasoning
	if !strings.EqualFold(strings.TrimSpace(result.Prediction), prediction) && result.Prediction != "" {
		reasoning = fmt.Sprintf("%s (model label %q was overridden by yes_probability=%.4f)", reasoning, result.Prediction, probability)
	}

	return &AIPrediction{
		Prediction:           prediction,
		Probability:          probability,
		Confidence:           confidence,
		Reasoning:            reasoning,
		KeyFactors:           result.KeyFactors,
		RiskAssessment:       result.RiskAssessment,
		TimeHorizon:          result.TimeHorizon,
		AlternativeScenarios: result.AlternativeScenarios,
	}, nil
}

func (ap *AIPredictor) BatchPredict(ctx context.Context, requests []*PredictionRequest) ([]*AIPrediction, error) {
	predictions := make([]*AIPrediction, 0, len(requests))

	for _, req := range requests {
		pred, err := ap.Predict(ctx, req)
		if err != nil {
			continue
		}
		predictions = append(predictions, pred)
	}

	return predictions, nil
}

func (ap *AIPredictor) GetCachedPrediction(marketID string) (*AIPrediction, bool) {
	return ap.cache.get(marketID)
}

func (ap *AIPredictor) ClearCache() {
	ap.cache.clear()
}

func (ap *AIPredictor) CompareWithMarket(prediction *AIPrediction, marketPrice float64) *ValueAssessment {
	if prediction == nil {
		return &ValueAssessment{Recommendation: "HOLD", ConfidenceLevel: "LOW"}
	}
	// marketPrice is a decimal probability in [0,1] (Polymarket quotes 0.58,
	// not 58 cents). Do not divide by 100 here.
	if marketPrice <= 0 || marketPrice >= 1 {
		return &ValueAssessment{
			MarketID:          prediction.MarketID,
			AIPrediction:      prediction.Prediction,
			AIProbability:     prediction.Probability,
			MarketImpliedProb: marketPrice,
			Recommendation:    "HOLD",
			ConfidenceLevel:   "LOW",
		}
	}

	aiProb := clampProbability(prediction.Probability)
	impliedProb := clampProbability(marketPrice)
	edge := aiProb - impliedProb
	edgePercent := 0.0
	if impliedProb > 0 {
		edgePercent = edge / impliedProb * 100
	}

	assessment := &ValueAssessment{
		MarketID:          prediction.MarketID,
		AIPrediction:      prediction.Prediction,
		AIProbability:     aiProb,
		MarketImpliedProb: impliedProb,
		Edge:              edge,
		EdgePercent:       edgePercent,
		Recommendation:    "HOLD",
		ConfidenceLevel:   "LOW",
	}

	if edge > 0.1 {
		assessment.Recommendation = "STRONG_BUY_YES"
		assessment.ConfidenceLevel = "HIGH"
	} else if edge > 0.05 {
		assessment.Recommendation = "BUY_YES"
		assessment.ConfidenceLevel = "MEDIUM"
	} else if edge < -0.1 {
		assessment.Recommendation = "STRONG_BUY_NO"
		assessment.ConfidenceLevel = "HIGH"
	} else if edge < -0.05 {
		assessment.Recommendation = "BUY_NO"
		assessment.ConfidenceLevel = "MEDIUM"
	}

	return assessment
}

type ValueAssessment struct {
	MarketID          string  `json:"market_id"`
	AIPrediction      string  `json:"ai_prediction"`
	AIProbability     float64 `json:"ai_probability"`
	MarketImpliedProb float64 `json:"market_implied_prob"`
	Edge              float64 `json:"edge"`
	EdgePercent       float64 `json:"edge_percent"`
	Recommendation    string  `json:"recommendation"`
	ConfidenceLevel   string  `json:"confidence_level"`
}

func (pc *PredictionCache) get(key string) (*AIPrediction, bool) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	pred, ok := pc.predictions[key]
	if !ok {
		return nil, false
	}

	if time.Since(pc.lastUpdate[key]) > pc.ttl {
		return nil, false
	}

	return pred, true
}

func (pc *PredictionCache) set(key string, prediction *AIPrediction) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.predictions[key] = prediction
	pc.lastUpdate[key] = time.Now()
}

func (pc *PredictionCache) clear() {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.predictions = make(map[string]*AIPrediction)
	pc.lastUpdate = make(map[string]time.Time)
}
