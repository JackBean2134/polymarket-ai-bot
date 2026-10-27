package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type LLMClient struct {
	apiKey      string
	baseURL     string
	model       string
	temperature float64
	maxTokens   int
	httpClient  *http.Client
}

type LLMRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMResponse struct {
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// DefaultBaseURL returns an OpenAI-compatible endpoint for known providers.
func DefaultBaseURL(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "deepseek":
		return "https://api.deepseek.com"
	case "qwen", "dashscope", "alibaba":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "openai":
		return "https://api.openai.com/v1"
	default:
		return "https://api.deepseek.com"
	}
}

func NewLLMClient(apiKey, baseURL, model string) *LLMClient {
	return NewLLMClientWithConfig(AIConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
}

func NewLLMClientWithConfig(config AIConfig) *LLMClient {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL(config.Provider)
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		if strings.EqualFold(config.Provider, "qwen") {
			model = "qwen-plus"
		} else {
			model = "deepseek-chat"
		}
	}
	temperature := config.Temperature
	if temperature <= 0 {
		temperature = 0.3
	}
	maxTokens := config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2000
	}
	timeout := time.Duration(config.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &LLMClient{
		apiKey:      config.APIKey,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		model:       model,
		temperature: temperature,
		maxTokens:   maxTokens,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *LLMClient) Chat(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return "", fmt.Errorf("LLM API key is not configured")
	}

	request := &LLMRequest{
		Model: c.model,
		Messages: []Message{
			{
				Role:    "system",
				Content: "You are a careful forecasting analyst for prediction markets. Output only a single JSON object. Probabilities must be P(YES) in [0,1], not percentages.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
		Temperature: c.temperature,
		MaxTokens:   c.maxTokens,
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}

		content, retry, err := c.doChat(ctx, requestBody)
		if err == nil {
			return content, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}

	return "", lastErr
}

func (c *LLMClient) doChat(ctx context.Context, requestBody []byte) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(requestBody))
	if err != nil {
		return "", false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", true, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", retry, fmt.Errorf("LLM API error: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var llmResp LLMResponse
	if err := json.Unmarshal(body, &llmResp); err != nil {
		return "", false, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if len(llmResp.Choices) == 0 {
		return "", false, fmt.Errorf("no choices in response")
	}

	return llmResp.Choices[0].Message.Content, false, nil
}
