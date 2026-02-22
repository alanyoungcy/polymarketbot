package crypto

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// --------------------------------------------------------------------------
// EIP-712 type hashes (pre-computed keccak256 of the canonical type strings).
// --------------------------------------------------------------------------

var (
	// EIP712Domain(string name,string version,uint256 chainId)
	eip712DomainTypeHash = ethcrypto.Keccak256(
		[]byte("EIP712Domain(string name,string version,uint256 chainId)"),
	)

	// ClobAuth(address address,uint256 timestamp,uint256 nonce)
	clobAuthTypeHash = ethcrypto.Keccak256(
		[]byte("ClobAuth(address address,uint256 timestamp,uint256 nonce)"),
	)

	// Order(uint256 salt,address maker,address signer,address taker,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint256 expiration,uint256 nonce,uint256 feeRateBps,uint8 side,uint8 signatureType)
	orderTypeHash = ethcrypto.Keccak256(
		[]byte("Order(uint256 salt,address maker,address signer,address taker,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint256 expiration,uint256 nonce,uint256 feeRateBps,uint8 side,uint8 signatureType)"),
	)
)

// OrderPayload represents the 12 fields of a Polymarket CLOB order that
// must be signed via EIP-712. String types are used for addresses and large
// numbers to preserve precision across JSON boundaries.
type OrderPayload struct {
	Salt          string `json:"salt"`
	Maker         string `json:"maker"`
	Signer        string `json:"signer"`
	Taker         string `json:"taker"`
	TokenID       string `json:"tokenId"`
	MakerAmount   string `json:"makerAmount"`
	TakerAmount   string `json:"takerAmount"`
	Expiration    string `json:"expiration"`
	Nonce         string `json:"nonce"`
	FeeRateBps    string `json:"feeRateBps"`
	Side          int    `json:"side"`          // 0 = BUY, 1 = SELL
	SignatureType int    `json:"signatureType"` // 0 = EOA, 1 = POLY_PROXY, 2 = POLY_GNOSIS_SAFE
}

// Signer provides EIP-712 signing for the Polymarket CLOB API.
type Signer struct {
	privateKey *ecdsa.PrivateKey
	address    common.Address
	chainID    int
	domainSep  []byte // cached EIP-712 domain separator hash
}

// NewSigner creates a Signer from a hex-encoded secp256k1 private key and
// the target chain ID (137 for Polygon mainnet, 80002 for Amoy testnet).
func NewSigner(privateKeyHex string, chainID int) (*Signer, error) {
	keyHex := strings.TrimPrefix(privateKeyHex, "0x")
	pk, err := ethcrypto.HexToECDSA(keyHex)
	if err != nil {
		return nil, fmt.Errorf("crypto/signer: invalid private key: %w", err)
	}

	addr := ethcrypto.PubkeyToAddress(pk.PublicKey)

	s := &Signer{
		privateKey: pk,
		address:    addr,
		chainID:    chainID,
	}

	// Pre-compute domain separator for the ClobAuth domain.
	s.domainSep = s.buildDomainSeparator("ClobAuthDomain", "1", chainID)

	return s, nil
}

// Address returns the Ethereum address derived from the signer's private key.
func (s *Signer) Address() common.Address {
	return s.address
}

// SignAuthMessage signs a ClobAuth EIP-712 message used to obtain an API key
// from the Polymarket CLOB. The returned string is a hex-encoded signature
// with recovery byte (65 bytes total).
func (s *Signer) SignAuthMessage(address string, timestamp, nonce int64) (string, error) {
	addr := common.HexToAddress(address)

	structHash := ethcrypto.Keccak256(
		concatBytes(
			clobAuthTypeHash,
			common.LeftPadBytes(addr.Bytes(), 32),
			bigIntTo32Bytes(big.NewInt(timestamp)),
			bigIntTo32Bytes(big.NewInt(nonce)),
		),
	)

	digest := eip712Hash(s.domainSep, structHash)
	return s.signDigest(digest)
}

// SignOrder signs an Order EIP-712 struct used to place limit orders on the
// Polymarket CLOB. It returns a hex-encoded 65-byte signature.
func (s *Signer) SignOrder(order OrderPayload) (string, error) {
	// Build the order domain separator (Exchange domain).
	domainSep := s.buildDomainSeparator("ClobAuthDomain", "1", s.chainID)

	structHash, err := orderStructHash(order)
	if err != nil {
		return "", err
	}

	digest := eip712Hash(domainSep, structHash)
	return s.signDigest(digest)
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// buildDomainSeparator returns keccak256(abi.encode(typeHash, nameHash, versionHash, chainId)).
func (s *Signer) buildDomainSeparator(name, version string, chainID int) []byte {
	return ethcrypto.Keccak256(
		concatBytes(
			eip712DomainTypeHash,
			ethcrypto.Keccak256([]byte(name)),
			ethcrypto.Keccak256([]byte(version)),
			bigIntTo32Bytes(big.NewInt(int64(chainID))),
		),
	)
}

// eip712Hash computes the final EIP-712 digest:
//
//	keccak256("\x19\x01" || domainSeparator || structHash)
func eip712Hash(domainSep, structHash []byte) []byte {
	return ethcrypto.Keccak256(
		concatBytes(
			[]byte{0x19, 0x01},
			domainSep,
			structHash,
		),
	)
}

// signDigest signs a 32-byte digest using secp256k1 and returns the
// hex-encoded signature (r || s || v, 65 bytes).
func (s *Signer) signDigest(digest []byte) (string, error) {
	sig, err := ethcrypto.Sign(digest, s.privateKey)
	if err != nil {
		return "", fmt.Errorf("crypto/signer: signing: %w", err)
	}

	// go-ethereum returns v in {0,1}; EIP-712 expects v in {27,28}.
	if sig[64] < 27 {
		sig[64] += 27
	}

	return "0x" + hex.EncodeToString(sig), nil
}

// orderStructHash encodes and hashes an OrderPayload according to EIP-712.
func orderStructHash(o OrderPayload) ([]byte, error) {
	salt, ok := new(big.Int).SetString(o.Salt, 10)
	if !ok {
		return nil, fmt.Errorf("crypto/signer: invalid salt %q", o.Salt)
	}
	tokenID, ok := new(big.Int).SetString(o.TokenID, 10)
	if !ok {
		return nil, fmt.Errorf("crypto/signer: invalid tokenId %q", o.TokenID)
	}
	makerAmt, ok := new(big.Int).SetString(o.MakerAmount, 10)
	if !ok {
		return nil, fmt.Errorf("crypto/signer: invalid makerAmount %q", o.MakerAmount)
	}
	takerAmt, ok := new(big.Int).SetString(o.TakerAmount, 10)
	if !ok {
		return nil, fmt.Errorf("crypto/signer: invalid takerAmount %q", o.TakerAmount)
	}
	expiration, ok := new(big.Int).SetString(o.Expiration, 10)
	if !ok {
		return nil, fmt.Errorf("crypto/signer: invalid expiration %q", o.Expiration)
	}
	nonce, ok := new(big.Int).SetString(o.Nonce, 10)
	if !ok {
		return nil, fmt.Errorf("crypto/signer: invalid nonce %q", o.Nonce)
	}
	feeRate, ok := new(big.Int).SetString(o.FeeRateBps, 10)
	if !ok {
		return nil, fmt.Errorf("crypto/signer: invalid feeRateBps %q", o.FeeRateBps)
	}

	maker := common.HexToAddress(o.Maker)
	signer := common.HexToAddress(o.Signer)
	taker := common.HexToAddress(o.Taker)

	return ethcrypto.Keccak256(
		concatBytes(
			orderTypeHash,
			bigIntTo32Bytes(salt),
			common.LeftPadBytes(maker.Bytes(), 32),
			common.LeftPadBytes(signer.Bytes(), 32),
			common.LeftPadBytes(taker.Bytes(), 32),
			bigIntTo32Bytes(tokenID),
			bigIntTo32Bytes(makerAmt),
			bigIntTo32Bytes(takerAmt),
			bigIntTo32Bytes(expiration),
			bigIntTo32Bytes(nonce),
			bigIntTo32Bytes(feeRate),
			bigIntTo32Bytes(big.NewInt(int64(o.Side))),
			bigIntTo32Bytes(big.NewInt(int64(o.SignatureType))),
		),
	), nil
}

// bigIntTo32Bytes returns a 32-byte big-endian representation of n.
func bigIntTo32Bytes(n *big.Int) []byte {
	b := n.Bytes()
	if len(b) >= 32 {
		return b[:32]
	}
	padded := make([]byte, 32)
	copy(padded[32-len(b):], b)
	return padded
}

// concatBytes concatenates multiple byte slices into one.
func concatBytes(slices ...[]byte) []byte {
	total := 0
	for _, s := range slices {
		total += len(s)
	}
	buf := make([]byte, 0, total)
	for _, s := range slices {
		buf = append(buf, s...)
	}
	return buf
}
