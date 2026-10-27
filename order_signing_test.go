package polymarket

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Official Polymarket python-order-utils test vector. If this signature
// matches byte-for-byte, the EIP-712 domain/struct encoding and deterministic
// secp256k1 signing match the reference implementation.
func TestOrderDigestMatchesOfficialVector(t *testing.T) {
	privateKey, err := crypto.HexToECDSA("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	if err != nil {
		t.Fatal(err)
	}

	maker := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	digest, err := orderDigest(
		"0xdFE02Eb6733538f8Ea35D585af8DE5958AD99E40",
		80002,
		big.NewInt(479249096354),
		maker,
		maker,
		common.Address{},
		big.NewInt(1234),
		big.NewInt(100000000),
		big.NewInt(50000000),
		big.NewInt(0),
		big.NewInt(0),
		big.NewInt(100),
		0,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}

	signature, err := crypto.Sign(digest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if signature[64] < 27 {
		signature[64] += 27
	}
	got := "0x" + hex.EncodeToString(signature)
	want := "0x302cd9abd0b5fcaa202a344437ec0b6660da984e24ae9ad915a592a90facf5a51bb8a873cd8d270f070217fea1986531d5eec66f1162a81f66e026db653bf7ce1c"
	if got != want {
		t.Fatalf("EIP-712 signature mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestOrderDigestMatchesOfficialNegRiskVector(t *testing.T) {
	privateKey, err := crypto.HexToECDSA("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	if err != nil {
		t.Fatal(err)
	}
	maker := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	digest, err := orderDigest(
		"0xC5d563A36AE78145C45a50134d48A1215220f80a",
		80002,
		big.NewInt(479249096354),
		maker,
		maker,
		common.Address{},
		big.NewInt(1234),
		big.NewInt(100000000),
		big.NewInt(50000000),
		big.NewInt(0),
		big.NewInt(0),
		big.NewInt(100),
		0,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := "0x"+hex.EncodeToString(digest), "0xf15790d3edc4b5aed427b0b543a9206fcf4b1a13dfed016d33bfb313076263b8"; got != want {
		t.Fatalf("negRisk digest mismatch\n got: %s\nwant: %s", got, want)
	}
	signature, err := crypto.Sign(digest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if signature[64] < 27 {
		signature[64] += 27
	}
	if got, want := "0x"+hex.EncodeToString(signature), "0x1b3646ef347e5bd144c65bd3357ba19c12c12abaeedae733cf8579bc51a2752c0454c3bc6b236957e393637982c769b8dc0706c0f5c399983d933850afd1cbcd1c"; got != want {
		t.Fatalf("negRisk signature mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestOrderSignerBuildsValidShape(t *testing.T) {
	signer, err := NewOrderSigner(OrderSignerConfig{
		PrivateKeyHex: "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
		ChainID:       ChainPolygon,
		SaltGenerator: func() uint64 { return 42 },
	})
	if err != nil {
		t.Fatal(err)
	}
	if signer.Address() != "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266" {
		t.Fatalf("unexpected signer address: %s", signer.Address())
	}

	order, err := signer.BuildSignedOrder("1234", "BUY", 100, 0.5, 0.01, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if order.Maker != signer.Funder() || order.Signer != signer.Address() {
		t.Fatalf("unexpected maker/signer: %+v", order)
	}
	if order.MakerAmount != "50000000" || order.TakerAmount != "100000000" {
		t.Fatalf("unexpected BUY amounts: maker=%s taker=%s", order.MakerAmount, order.TakerAmount)
	}
	if order.Side != "BUY" || order.SignatureType != 0 || order.Salt != 42 {
		t.Fatalf("unexpected order fields: %+v", order)
	}
	if len(order.Signature) != 132 || order.OrderHash == "" {
		t.Fatalf("signature/hash missing: %+v", order)
	}
}
