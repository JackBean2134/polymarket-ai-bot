package polymarket

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const (
	// Polygon mainnet contracts used by Polymarket. USDC.e is the collateral
	// asset (confirmed by Gamma's assetAddress); CTF is the ERC-1155 ledger.
	PolygonUSDCE = "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"
	PolygonCTF   = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"

	erc20AllowanceSelector        = "0xdd62ed3e" // allowance(address,address)
	erc20BalanceOfSelector        = "0x70a08231" // balanceOf(address)
	erc1155ApprovedForAllSelector = "0xe985e9c5" // isApprovedForAll(address,address)

	// DefaultPolygonRPC is a keyless public endpoint. polygon-rpc.com now
	// returns 401, so PublicNode is the default; override with POLYGON_RPC_URL.
	DefaultPolygonRPC = "https://polygon-bor-rpc.publicnode.com"
)

// EthCaller is the minimal JSON-RPC surface the allowance checks need.
type EthCaller interface {
	EthCall(ctx context.Context, to string, data string) (string, error)
}

// RPCClient is a tiny eth_call client. It never signs and never sends a
// transaction.
type RPCClient struct {
	url    string
	client *http.Client
}

func NewRPCClient(url string) *RPCClient {
	url = strings.TrimSpace(url)
	if url == "" {
		url = DefaultPolygonRPC
	}
	return &RPCClient{url: url, client: &http.Client{Timeout: 15 * time.Second}}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result string    `json:"result"`
	Error  *rpcError `json:"error"`
}

func (c *RPCClient) EthCall(ctx context.Context, to string, data string) (string, error) {
	payload, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "eth_call",
		Params:  []interface{}{map[string]string{"to": to, "data": data}, "latest"},
	})
	if err != nil {
		return "", fmt.Errorf("encode rpc request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build rpc request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("rpc request: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read rpc response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("rpc returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded rpcResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("decode rpc response: %w", err)
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	return decoded.Result, nil
}

// AllowanceResult summarises the on-chain state a live order needs: approval,
// collateral balance and CTF operator approval.
type AllowanceResult struct {
	ChainID        int64   `json:"chain_id"`
	Owner          string  `json:"owner"`
	Exchange       string  `json:"exchange"`
	USDCAllowance  float64 `json:"usdc_allowance"`
	USDCBalance    float64 `json:"usdc_balance"`
	RequiredUSD    float64 `json:"required_usd"`
	USDCSufficient bool    `json:"usdc_allowance_sufficient"`
	BalanceEnough  bool    `json:"usdc_balance_sufficient"`
	CTFApproved    bool    `json:"ctf_approved_for_all"`
}

// CheckTradingAllowances reads USDC allowance and CTF operator approval for the
// funder/exchange pair. Read-only: no key, no transaction.
func CheckTradingAllowances(ctx context.Context, caller EthCaller, chainID int64, owner, exchange string, requiredUSD float64) (*AllowanceResult, error) {
	if caller == nil {
		return nil, fmt.Errorf("no RPC caller configured")
	}
	if chainID != ChainPolygon {
		return nil, fmt.Errorf("allowance checks are implemented for Polygon mainnet (137) only, got chain %d", chainID)
	}
	owner = strings.TrimSpace(owner)
	exchange = strings.TrimSpace(exchange)
	if err := validateHexAddress(owner); err != nil {
		return nil, fmt.Errorf("owner: %w", err)
	}
	if err := validateHexAddress(exchange); err != nil {
		return nil, fmt.Errorf("exchange: %w", err)
	}

	allowanceData, err := encodeTwoAddressCall(erc20AllowanceSelector, owner, exchange)
	if err != nil {
		return nil, err
	}
	allowanceHex, err := caller.EthCall(ctx, PolygonUSDCE, allowanceData)
	if err != nil {
		return nil, fmt.Errorf("read USDC allowance: %w", err)
	}
	allowance, err := parseHexUint256(allowanceHex)
	if err != nil {
		return nil, fmt.Errorf("parse USDC allowance: %w", err)
	}

	approvalData, err := encodeTwoAddressCall(erc1155ApprovedForAllSelector, owner, exchange)
	if err != nil {
		return nil, err
	}
	approvalHex, err := caller.EthCall(ctx, PolygonCTF, approvalData)
	if err != nil {
		return nil, fmt.Errorf("read CTF approval: %w", err)
	}
	approved, err := parseHexUint256(approvalHex)
	if err != nil {
		return nil, fmt.Errorf("parse CTF approval: %w", err)
	}

	balanceData, err := encodeSingleAddressCall(erc20BalanceOfSelector, owner)
	if err != nil {
		return nil, err
	}
	balanceHex, err := caller.EthCall(ctx, PolygonUSDCE, balanceData)
	if err != nil {
		return nil, fmt.Errorf("read USDC balance: %w", err)
	}
	balance, err := parseHexUint256(balanceHex)
	if err != nil {
		return nil, fmt.Errorf("parse USDC balance: %w", err)
	}

	allowanceUSD := usdcToFloat(allowance)
	balanceUSD := usdcToFloat(balance)

	return &AllowanceResult{
		ChainID:        chainID,
		Owner:          owner,
		Exchange:       exchange,
		USDCAllowance:  allowanceUSD,
		USDCBalance:    balanceUSD,
		RequiredUSD:    requiredUSD,
		USDCSufficient: allowanceUSD+1e-9 >= requiredUSD,
		BalanceEnough:  balanceUSD+1e-9 >= requiredUSD,
		CTFApproved:    approved.Sign() != 0,
	}, nil
}

func usdcToFloat(amount *big.Int) float64 {
	value, _ := new(big.Float).Quo(new(big.Float).SetInt(amount), big.NewFloat(1e6)).Float64()
	return value
}

func validateHexAddress(address string) error {
	trimmed := strings.TrimPrefix(strings.TrimSpace(address), "0x")
	if len(trimmed) != 40 {
		return fmt.Errorf("%q is not a 40-character hex address", address)
	}
	if _, err := hex.DecodeString(trimmed); err != nil {
		return fmt.Errorf("%q is not valid hex: %w", address, err)
	}
	return nil
}

func encodeTwoAddressCall(selector, first, second string) (string, error) {
	a, err := encodeAddressArg(first)
	if err != nil {
		return "", err
	}
	b, err := encodeAddressArg(second)
	if err != nil {
		return "", err
	}
	return selector + a + b, nil
}

func encodeSingleAddressCall(selector, address string) (string, error) {
	arg, err := encodeAddressArg(address)
	if err != nil {
		return "", err
	}
	return selector + arg, nil
}

func encodeAddressArg(address string) (string, error) {
	if err := validateHexAddress(address); err != nil {
		return "", err
	}
	trimmed := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(address), "0x"))
	return strings.Repeat("0", 24) + trimmed, nil
}

func parseHexUint256(value string) (*big.Int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "0x" || trimmed == "0x0" {
		return big.NewInt(0), nil
	}
	trimmed = strings.TrimPrefix(trimmed, "0x")
	number, ok := new(big.Int).SetString(trimmed, 16)
	if !ok {
		return nil, fmt.Errorf("invalid hex quantity %q", value)
	}
	return number, nil
}
