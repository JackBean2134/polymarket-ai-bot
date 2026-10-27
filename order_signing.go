package polymarket

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	SignatureTypeEOA            = 0
	SignatureTypePolyProxy      = 1
	SignatureTypePolyGnosisSafe = 2

	ChainPolygon int64 = 137
	ChainAmoy    int64 = 80002
)

var (
	orderTypeHash  = crypto.Keccak256Hash([]byte("Order(uint256 salt,address maker,address signer,address taker,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint256 expiration,uint256 nonce,uint256 feeRateBps,uint8 side,uint8 signatureType)"))
	domainTypeHash = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	domainNameHash = crypto.Keccak256Hash([]byte("Polymarket CTF Exchange"))
	domainVerHash  = crypto.Keccak256Hash([]byte("1"))
	zeroAddress    = common.Address{}
)

type exchangeContracts struct {
	exchange string
	negRisk  string
}

var contractsByChain = map[int64]exchangeContracts{
	ChainPolygon: {
		exchange: "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E",
		negRisk:  "0xC5d563A36AE78145C45a50134d48A1215220f80a",
	},
	ChainAmoy: {
		exchange: "0xdFE02Eb6733538f8Ea35D585af8DE5958AD99E40",
		negRisk:  "0xC5d563A36AE78145C45a50134d48A1215220f80a",
	},
}

// OrderSigner builds and signs Polymarket CTF Exchange orders using EIP-712.
// The private key never leaves this struct and is never serialized.
type OrderSigner struct {
	privateKey    *ecdsa.PrivateKey
	signerAddress common.Address
	funder        common.Address
	chainID       int64
	signatureType int
	saltGenerator func() uint64
}

// SignedOrder is the JSON shape accepted by POST /order (inside the "order"
// field). Numeric amounts are decimal strings in token base units (1e6).
type SignedOrder struct {
	Salt          uint64 `json:"salt"`
	Maker         string `json:"maker"`
	Signer        string `json:"signer"`
	Taker         string `json:"taker"`
	TokenID       string `json:"tokenId"`
	MakerAmount   string `json:"makerAmount"`
	TakerAmount   string `json:"takerAmount"`
	Expiration    string `json:"expiration"`
	Nonce         string `json:"nonce"`
	FeeRateBps    string `json:"feeRateBps"`
	Side          string `json:"side"`
	SignatureType int    `json:"signatureType"`
	Signature     string `json:"signature"`
	OrderHash     string `json:"-"`
}

// OrderSignerConfig describes the wallet that signs and funds the order.
// signatureType 0 = EOA, 1 = Polymarket proxy, 2 = Polymarket Gnosis safe.
type OrderSignerConfig struct {
	PrivateKeyHex string
	ChainID       int64
	FunderAddress string
	SignatureType int
	// SaltGenerator is injectable for deterministic tests.
	SaltGenerator func() uint64
}

func NewOrderSigner(config OrderSignerConfig) (*OrderSigner, error) {
	keyHex := strings.TrimPrefix(strings.TrimSpace(config.PrivateKeyHex), "0x")
	if keyHex == "" {
		return nil, fmt.Errorf("private key is required for live order signing")
	}
	privateKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	signerAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

	chainID := config.ChainID
	if chainID == 0 {
		chainID = ChainPolygon
	}
	if _, ok := contractsByChain[chainID]; !ok {
		return nil, fmt.Errorf("unsupported chain ID %d", chainID)
	}

	signatureType := config.SignatureType
	if signatureType < SignatureTypeEOA || signatureType > SignatureTypePolyGnosisSafe {
		return nil, fmt.Errorf("signature type must be 0, 1 or 2")
	}

	funder := signerAddress
	if config.FunderAddress != "" {
		if !common.IsHexAddress(config.FunderAddress) {
			return nil, fmt.Errorf("invalid funder address %q", config.FunderAddress)
		}
		funder = common.HexToAddress(config.FunderAddress)
	}
	if signatureType == SignatureTypeEOA && funder != signerAddress {
		return nil, fmt.Errorf("EOA signature type requires funder == signer (%s)", signerAddress.Hex())
	}
	if signatureType != SignatureTypeEOA && config.FunderAddress == "" {
		return nil, fmt.Errorf("funder address is required for signature type %d", signatureType)
	}

	saltGenerator := config.SaltGenerator
	if saltGenerator == nil {
		saltGenerator = randomSalt
	}

	return &OrderSigner{
		privateKey:    privateKey,
		signerAddress: signerAddress,
		funder:        funder,
		chainID:       chainID,
		signatureType: signatureType,
		saltGenerator: saltGenerator,
	}, nil
}

func randomSalt() uint64 {
	limit := new(big.Int).Lsh(big.NewInt(1), 53)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return uint64(time.Now().UnixNano()) & ((1 << 53) - 1)
	}
	return value.Uint64()
}

func (s *OrderSigner) Address() string {
	return s.signerAddress.Hex()
}

func (s *OrderSigner) Funder() string {
	return s.funder.Hex()
}

// BuildSignedOrder creates a limit order. shares is denominated in conditional
// tokens (not USD); price is decimal USDC per share. The official rounding
// rules are applied based on the market tick size.
func (s *OrderSigner) BuildSignedOrder(tokenID, side string, shares, price, tickSize float64, feeRateBps int, negRisk bool) (*SignedOrder, error) {
	side = strings.ToUpper(strings.TrimSpace(side))
	if side != "BUY" && side != "SELL" {
		return nil, fmt.Errorf("side must be BUY or SELL")
	}
	if !validPrice(price) {
		return nil, fmt.Errorf("price %f is outside (0,1)", price)
	}
	if shares <= 0 {
		return nil, fmt.Errorf("shares must be positive")
	}
	if tickSize <= 0 {
		tickSize = 0.001
	}
	if price < tickSize-1e-12 || price > 1-tickSize+1e-12 {
		return nil, fmt.Errorf("price %f is outside tick-aligned range [%v, %v]", price, tickSize, 1-tickSize)
	}

	priceDecimals, sizeDecimals, amountDecimals, ok := roundingConfig(tickSize)
	if !ok {
		return nil, fmt.Errorf("unsupported tick size %v", tickSize)
	}

	rawPrice := roundNormal(price, priceDecimals)
	rawShares := roundDown(shares, sizeDecimals)
	if rawShares <= 0 {
		return nil, fmt.Errorf("shares round down to zero")
	}

	var makerAmount, takerAmount float64
	if side == "BUY" {
		makerAmount = roundedAmount(rawShares*rawPrice, amountDecimals)
		takerAmount = rawShares
	} else {
		makerAmount = rawShares
		takerAmount = roundedAmount(rawShares*rawPrice, amountDecimals)
	}

	makerUnits := toTokenUnits(makerAmount)
	takerUnits := toTokenUnits(takerAmount)
	if makerUnits <= 0 || takerUnits <= 0 {
		return nil, fmt.Errorf("order amounts round to zero")
	}

	token, ok := new(big.Int).SetString(strings.TrimSpace(tokenID), 10)
	if !ok || token.Sign() <= 0 {
		return nil, fmt.Errorf("invalid token ID %q", tokenID)
	}

	sideValue := uint8(0)
	if side == "SELL" {
		sideValue = 1
	}

	contract := contractsByChain[s.chainID]
	exchangeAddress := contract.exchange
	if negRisk {
		exchangeAddress = contract.negRisk
	}

	salt := s.saltGenerator()
	order := &SignedOrder{
		Salt:          salt,
		Maker:         s.funder.Hex(),
		Signer:        s.signerAddress.Hex(),
		Taker:         zeroAddress.Hex(),
		TokenID:       token.String(),
		MakerAmount:   strconv.FormatInt(makerUnits, 10),
		TakerAmount:   strconv.FormatInt(takerUnits, 10),
		Expiration:    "0",
		Nonce:         "0",
		FeeRateBps:    strconv.Itoa(feeRateBps),
		Side:          side,
		SignatureType: s.signatureType,
	}

	digest, err := orderDigest(
		exchangeAddress,
		s.chainID,
		new(big.Int).SetUint64(salt),
		s.funder,
		s.signerAddress,
		zeroAddress,
		token,
		new(big.Int).SetInt64(makerUnits),
		new(big.Int).SetInt64(takerUnits),
		big.NewInt(0), // expiration
		big.NewInt(0), // nonce
		big.NewInt(int64(feeRateBps)),
		sideValue,
		uint8(s.signatureType),
	)
	if err != nil {
		return nil, err
	}

	signature, err := crypto.Sign(digest, s.privateKey)
	if err != nil {
		return nil, fmt.Errorf("sign order: %w", err)
	}
	// Ethereum signatures use V=27/28; go-ethereum returns recovery id 0/1.
	if signature[64] < 27 {
		signature[64] += 27
	}
	order.Signature = "0x" + hex.EncodeToString(signature)
	order.OrderHash = "0x" + hex.EncodeToString(digest)
	return order, nil
}

func orderDigest(
	contractAddress string,
	chainID int64,
	salt *big.Int,
	maker, signer, taker common.Address,
	tokenID, makerAmount, takerAmount, expiration, nonce, feeRateBps *big.Int,
	side, signatureType uint8,
) ([]byte, error) {
	if !common.IsHexAddress(contractAddress) {
		return nil, fmt.Errorf("invalid exchange address %q", contractAddress)
	}
	verifyingContract := common.HexToAddress(contractAddress)
	if maker == zeroAddress || signer == zeroAddress {
		return nil, fmt.Errorf("maker and signer must be non-zero")
	}

	domain := make([]byte, 0, 32*5)
	domain = append(domain, domainTypeHash.Bytes()...)
	domain = append(domain, domainNameHash.Bytes()...)
	domain = append(domain, domainVerHash.Bytes()...)
	domain = append(domain, uint256Word(big.NewInt(chainID))...)
	domain = append(domain, addressWord(verifyingContract)...)
	domainSeparator := crypto.Keccak256(domain)

	encoded := make([]byte, 0, 32*13)
	encoded = append(encoded, orderTypeHash.Bytes()...)
	encoded = append(encoded, uint256Word(salt)...)
	encoded = append(encoded, addressWord(maker)...)
	encoded = append(encoded, addressWord(signer)...)
	encoded = append(encoded, addressWord(taker)...)
	encoded = append(encoded, uint256Word(tokenID)...)
	encoded = append(encoded, uint256Word(makerAmount)...)
	encoded = append(encoded, uint256Word(takerAmount)...)
	encoded = append(encoded, uint256Word(expiration)...)
	encoded = append(encoded, uint256Word(nonce)...)
	encoded = append(encoded, uint256Word(feeRateBps)...)
	encoded = append(encoded, uint256Word(new(big.Int).SetUint64(uint64(side)))...)
	encoded = append(encoded, uint256Word(new(big.Int).SetUint64(uint64(signatureType)))...)
	structHash := crypto.Keccak256(encoded)

	digest := make([]byte, 0, 66)
	digest = append(digest, 0x19, 0x01)
	digest = append(digest, domainSeparator...)
	digest = append(digest, structHash...)
	return crypto.Keccak256(digest), nil
}

func uint256Word(value *big.Int) []byte {
	if value == nil || value.Sign() < 0 {
		value = big.NewInt(0)
	}
	bytes := value.Bytes()
	if len(bytes) > 32 {
		bytes = bytes[len(bytes)-32:]
	}
	return common.LeftPadBytes(bytes, 32)
}

func addressWord(address common.Address) []byte {
	return common.LeftPadBytes(address.Bytes(), 32)
}

func roundingConfig(tickSize float64) (priceDecimals, sizeDecimals, amountDecimals int, ok bool) {
	switch {
	case tickSize >= 0.1:
		return 1, 2, 3, true
	case tickSize >= 0.01:
		return 2, 2, 4, true
	case tickSize >= 0.001:
		return 3, 2, 5, true
	case tickSize >= 0.0001:
		return 4, 2, 6, true
	default:
		return 0, 0, 0, false
	}
}

func roundedAmount(value float64, decimals int) float64 {
	if decimalPlaces(value) > decimals {
		value = roundUp(value, decimals+4)
		if decimalPlaces(value) > decimals {
			value = roundDown(value, decimals)
		}
	}
	return value
}

func roundNormal(value float64, decimals int) float64 {
	factor := math.Pow10(decimals)
	return math.Round(value*factor) / factor
}

func roundDown(value float64, decimals int) float64 {
	factor := math.Pow10(decimals)
	return math.Floor(value*factor) / factor
}

func roundUp(value float64, decimals int) float64 {
	factor := math.Pow10(decimals)
	return math.Ceil(value*factor) / factor
}

func decimalPlaces(value float64) int {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	index := strings.IndexByte(text, '.')
	if index < 0 {
		return 0
	}
	return len(text) - index - 1
}

func toTokenUnits(value float64) int64 {
	units := value * 1e6
	if decimalPlaces(units) > 0 {
		units = math.Round(units)
	}
	return int64(units)
}
