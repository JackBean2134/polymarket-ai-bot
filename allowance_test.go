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
	err       error
	calls     []string
}

func (f *fakeEthCaller) EthCall(ctx context.Context, to string, data string) (string, error) {
	f.calls = append(f.calls, to+":"+data)
	if f.err != nil {
		return "", f.err
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
	if len(caller.calls) != 2 {
		t.Fatalf("expected two eth_calls, got %d", len(caller.calls))
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
