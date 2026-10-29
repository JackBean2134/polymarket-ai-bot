package polymarket

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
)

type fakeEthCaller struct {
	responses map[string]string
	handler   func(to, data string) string
	err       error
	calls     []string
}

func (f *fakeEthCaller) EthCall(ctx context.Context, to string, data string) (string, error) {
	f.calls = append(f.calls, to+":"+data)
	if f.err != nil {
		return "", f.err
	}
	if f.handler != nil {
		return f.handler(to, data), nil
	}
	if value, ok := f.responses[to]; ok {
		return value, nil
	}
	return "0x0", nil
}

func TestEncodeTwoAddressCall(t *testing.T) {
	owner := "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"
	spender := "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E"
	data, err := encodeTwoAddressCall(erc20AllowanceSelector, owner, spender)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := erc20AllowanceSelector +
		"000000000000000000000000" + strings.ToLower(strings.TrimPrefix(owner, "0x")) +
		"000000000000000000000000" + strings.ToLower(strings.TrimPrefix(spender, "0x"))
	if data != want {
		t.Fatalf("unexpected calldata:\n got %s\nwant %s", data, want)
	}
	if len(data) != 2+8+64+64 {
		t.Fatalf("unexpected calldata length: %d", len(data))
	}
	if _, err := encodeTwoAddressCall(erc20AllowanceSelector, "0x1234", spender); err == nil {
		t.Fatal("short address must be rejected")
	}
}

func TestParseHexUint256(t *testing.T) {
	cases := map[string]int64{
		"":                  0,
		"0x":                0,
		"0x0":               0,
		"0x4c4b40":          5000000,
		"0xde0b6b3a7640000": 1000000000000000000,
	}
	for input, want := range cases {
		got, err := parseHexUint256(input)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if got.Cmp(big.NewInt(want)) != 0 {
			t.Fatalf("%q: expected %d, got %s", input, want, got)
		}
	}
	if _, err := parseHexUint256("0xzz"); err == nil {
		t.Fatal("invalid hex must be rejected")
	}
}

func TestCheckTradingAllowances(t *testing.T) {
	owner := "0x00000000000000000000000000000000000000aa"
	exchange := "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E"

	caller := &fakeEthCaller{responses: map[string]string{
		PolygonUSDCE: "0x4c4b40", // 5.000000 USDC
		PolygonCTF:   "0x1",      // approved
	}}
	result, err := CheckTradingAllowances(context.Background(), caller, ChainPolygon, owner, exchange, 4.5)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.USDCSufficient || result.USDCAllowance != 5 || result.RequiredUSD != 4.5 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !result.CTFApproved {
		t.Fatalf("expected CTF approval: %+v", result)
	}
	if len(caller.calls) != 3 {
		t.Fatalf("expected three eth_calls (allowance, approval, balance), got %d", len(caller.calls))
	}

	caller = &fakeEthCaller{responses: map[string]string{PolygonUSDCE: "0x0", PolygonCTF: "0x0"}}
	result, err = CheckTradingAllowances(context.Background(), caller, ChainPolygon, owner, exchange, 4.5)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if result.USDCSufficient || result.CTFApproved {
		t.Fatalf("zero allowances must be reported as insufficient: %+v", result)
	}

	if _, err := CheckTradingAllowances(context.Background(), &fakeEthCaller{err: errors.New("rpc down")}, ChainPolygon, owner, exchange, 1); err == nil {
		t.Fatal("rpc errors must surface")
	}
	if _, err := CheckTradingAllowances(context.Background(), &fakeEthCaller{}, ChainAmoy, owner, exchange, 1); err == nil {
		t.Fatal("non-Polygon chains must be rejected")
	}
	if _, err := CheckTradingAllowances(context.Background(), &fakeEthCaller{}, ChainPolygon, "0xbad", exchange, 1); err == nil {
		t.Fatal("invalid owner must be rejected")
	}
}

func TestCheckTradingAllowancesDistinguishesBalance(t *testing.T) {
	owner := "0x00000000000000000000000000000000000000aa"
	exchange := "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E"

	caller := &fakeEthCaller{handler: func(to, data string) string {
		if to == PolygonUSDCE && strings.HasPrefix(data, erc20BalanceOfSelector) {
			return "0x0" // empty wallet
		}
		if to == PolygonUSDCE && strings.HasPrefix(data, erc20AllowanceSelector) {
			return "0x4c4b40" // 5 USDC approved
		}
		return "0x1"
	}}

	result, err := CheckTradingAllowances(context.Background(), caller, ChainPolygon, owner, exchange, 4.5)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.USDCSufficient {
		t.Fatalf("allowance should be sufficient: %+v", result)
	}
	if result.BalanceEnough || result.USDCBalance != 0 {
		t.Fatalf("balance should be insufficient: %+v", result)
	}
	if len(caller.calls) != 3 {
		t.Fatalf("expected three calls, got %d", len(caller.calls))
	}
}

func TestEncodeUint256Arg(t *testing.T) {
	cases := map[string]string{
		"0":   strings.Repeat("0", 64),
		"1":   strings.Repeat("0", 63) + "1",
		"255": strings.Repeat("0", 62) + "ff",
		"20006732765674855733524007935991362439352594042415161039767275461950712817548": "",
	}
	for input, want := range cases {
		got, err := encodeUint256Arg(input)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if len(got) != 64 {
			t.Fatalf("%q: expected 64 hex chars, got %d", input, len(got))
		}
		if want != "" && got != want {
			t.Fatalf("%q: expected %s, got %s", input, want, got)
		}
	}
	for _, bad := range []string{"", "-1", "0x10", "abc", strings.Repeat("9", 90)} {
		if _, err := encodeUint256Arg(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

func TestCheckCTFBalance(t *testing.T) {
	owner := "0x00000000000000000000000000000000000000aa"
	tokenID := "12345"
	// 12.345678 shares in 6-decimal units.
	caller := &fakeEthCaller{handler: func(to, data string) string {
		if to != PolygonCTF {
			t.Fatalf("unexpected contract %s", to)
		}
		if !strings.HasPrefix(data, erc1155BalanceOfSelector) {
			t.Fatalf("unexpected selector in %s", data)
		}
		return "0xbc614e" // 12345678
	}}

	shares, err := CheckCTFBalance(context.Background(), caller, ChainPolygon, owner, tokenID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if shares < 12.345677 || shares > 12.345679 {
		t.Fatalf("expected 12.345678 shares, got %f", shares)
	}

	if _, err := CheckCTFBalance(context.Background(), &fakeEthCaller{}, ChainAmoy, owner, tokenID); err == nil {
		t.Fatal("non-Polygon chains must be rejected")
	}
	if _, err := CheckCTFBalance(context.Background(), &fakeEthCaller{}, ChainPolygon, owner, "not-a-number"); err == nil {
		t.Fatal("invalid token id must be rejected")
	}
}

func TestValidateHexAddress(t *testing.T) {
	if err := validateHexAddress("0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"); err != nil {
		t.Fatalf("valid address rejected: %v", err)
	}
	for _, bad := range []string{"", "0x1234", "2791Bca1f2de4661ED88A30C99A7a9449Aa84174zz", "0x" + strings.Repeat("g", 40)} {
		if err := validateHexAddress(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}
