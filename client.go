package polymarket

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

const (
	GammaAPIBaseURL = "https://gamma-api.polymarket.com"
	DataAPIBaseURL  = "https://data-api.polymarket.com"
	CLOBAPIBaseURL  = "https://clob.polymarket.com"

	userAgent = "polymarket-ai-bot/0.2 (research)"
)

type Client struct {
	gammaClient   *resty.Client
	dataClient    *resty.Client
	clobClient    *resty.Client
	apiKey        string
	apiSecret     string
	passphrase    string
	walletAddress string
	retryCount    int
	retryWait     time.Duration
}

type ClientConfig struct {
	APIKey        string
	APISecret     string
	Passphrase    string
	WalletAddress string
	Timeout       time.Duration
	RetryCount    int
	RetryWait     time.Duration
}

func NewClient(config ClientConfig) *Client {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	retryCount := config.RetryCount
	if retryCount == 0 {
		retryCount = 3
	}
	if retryCount < 0 {
		retryCount = 0
	}

	retryWait := config.RetryWait
	if retryWait <= 0 {
		retryWait = 500 * time.Millisecond
	}

	newResty := func(baseURL string) *resty.Client {
		return resty.New().
			SetBaseURL(baseURL).
			SetTimeout(timeout).
			SetRetryCount(0).
			SetHeader("Accept", "application/json").
			SetHeader("User-Agent", userAgent)
	}

	return &Client{
		apiKey:        config.APIKey,
		apiSecret:     config.APISecret,
		passphrase:    config.Passphrase,
		walletAddress: config.WalletAddress,
		retryCount:    retryCount,
		retryWait:     retryWait,
		gammaClient:   newResty(GammaAPIBaseURL),
		dataClient:    newResty(DataAPIBaseURL),
		clobClient:    newResty(CLOBAPIBaseURL),
	}
}

// l2Headers builds the official Polymarket CLOB L2 authentication headers.
// The secret is used as the HMAC key and is never transmitted.
func (c *Client) l2Headers(method, requestPath, body string) map[string]string {
	if c.apiKey == "" || c.apiSecret == "" || c.walletAddress == "" {
		return nil
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	message := timestamp + strings.ToUpper(method) + requestPath + body

	secret, err := base64.URLEncoding.DecodeString(c.apiSecret)
	if err != nil {
		secret, err = base64.RawURLEncoding.DecodeString(c.apiSecret)
	}
	if err != nil {
		return nil
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(message))
	signature := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	headers := map[string]string{
		"POLY_ADDRESS":   c.walletAddress,
		"POLY_SIGNATURE": signature,
		"POLY_TIMESTAMP": timestamp,
		"POLY_API_KEY":   c.apiKey,
	}
	if c.passphrase != "" {
		headers["POLY_PASSPHRASE"] = c.passphrase
	}
	return headers
}

func (c *Client) executeRequest(ctx context.Context, client *resty.Client, method, endpoint string, params, body, result interface{}) error {
	var queryValues url.Values
	if params != nil {
		switch typed := params.(type) {
		case map[string]string:
			queryValues = url.Values{}
			for key, value := range typed {
				if value != "" {
					queryValues.Set(key, value)
				}
			}
		case url.Values:
			queryValues = typed
		default:
			return fmt.Errorf("unsupported query parameter type %T", params)
		}
	}

	var bodyBytes []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		bodyBytes = encoded
	}

	// Only GET requests are retried. Retrying an order POST can duplicate a
	// fill; retrying a DELETE is harmless but the exchange response is
	// authoritative, so we avoid hidden retries there too.
	attempts := 1
	if method == http.MethodGet {
		attempts = c.retryCount + 1
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			wait := c.retryWait * time.Duration(attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}

		req := client.R().SetContext(ctx)
		if len(queryValues) > 0 {
			req.SetQueryParamsFromValues(queryValues)
		}
		if bodyBytes != nil {
			req.SetHeader("Content-Type", "application/json")
			req.SetBody(bodyBytes)
		}
		if client == c.clobClient {
			pathForSign := endpoint
			if len(queryValues) > 0 {
				pathForSign += "?" + queryValues.Encode()
			}
			for key, value := range c.l2Headers(method, pathForSign, string(bodyBytes)) {
				req.SetHeader(key, value)
			}
		}

		var (
			resp *resty.Response
			err  error
		)
		switch method {
		case http.MethodGet:
			resp, err = req.Get(endpoint)
		case http.MethodPost:
			resp, err = req.Post(endpoint)
		case http.MethodDelete:
			resp, err = req.Delete(endpoint)
		default:
			return fmt.Errorf("unsupported HTTP method: %s", method)
		}

		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		if resp.IsSuccess() {
			if result != nil && len(resp.Body()) > 0 {
				if err := json.Unmarshal(resp.Body(), result); err != nil {
					return fmt.Errorf("failed to unmarshal response: %w", err)
				}
			}
			return nil
		}

		preview := string(resp.Body())
		if len(preview) > 512 {
			preview = preview[:512] + "..."
		}
		lastErr = fmt.Errorf("API error: status=%d, body=%s", resp.StatusCode(), preview)
		if resp.StatusCode() != http.StatusTooManyRequests && resp.StatusCode() < 500 {
			break
		}
	}

	return lastErr
}

func (c *Client) get(ctx context.Context, client *resty.Client, endpoint string, params, result interface{}) error {
	return c.executeRequest(ctx, client, http.MethodGet, endpoint, params, nil, result)
}

func (c *Client) post(ctx context.Context, client *resty.Client, endpoint string, body, result interface{}) error {
	return c.executeRequest(ctx, client, http.MethodPost, endpoint, nil, body, result)
}

func (c *Client) delete(ctx context.Context, client *resty.Client, endpoint string, body, result interface{}) error {
	return c.executeRequest(ctx, client, http.MethodDelete, endpoint, nil, body, result)
}

func (c *Client) GetGammaClient() *resty.Client {
	return c.gammaClient
}

func (c *Client) GetDataClient() *resty.Client {
	return c.dataClient
}

func (c *Client) GetCLOBClient() *resty.Client {
	return c.clobClient
}

func (c *Client) APIKey() string {
	return c.apiKey
}

func (c *Client) HasL2Credentials() bool {
	return c.apiKey != "" && c.apiSecret != "" && c.walletAddress != ""
}

type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API Error %d: %s", e.Code, e.Message)
}

type PaginationParams struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type PaginationResponse struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more,omitempty"`
}

func readResponseBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, nil
}
