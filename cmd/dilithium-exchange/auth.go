package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	nonceExpiry   = 10 * time.Minute
	sessionExpiry = 24 * time.Hour
)

// JWTClaims contains the JWT payload for authenticated exchange sessions.
type JWTClaims struct {
	UserID     int64  `json:"uid"`
	ETHAddress string `json:"eth"`
	jwt.RegisteredClaims
}

// GenerateNonce creates a random hex nonce for SIWE challenges.
func GenerateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// BuildSIWEMessage constructs the EIP-4361 Sign-In With Ethereum message.
// MetaMask and other wallets display this to the user before signing.
func BuildSIWEMessage(ethAddr, nonce string) string {
	now := time.Now().UTC().Format(time.RFC3339)
	return fmt.Sprintf(
		"exchange.dilithiumcoin.com wants you to sign in with your Ethereum account:\n"+
			"%s\n\n"+
			"Sign in to Dilithium Exchange\n\n"+
			"URI: https://exchange.dilithiumcoin.com\n"+
			"Version: 1\n"+
			"Chain ID: 1\n"+
			"Nonce: %s\n"+
			"Issued At: %s",
		ethAddr, nonce, now,
	)
}

// GenerateJWT creates a signed JWT for an authenticated user.
func GenerateJWT(userID int64, ethAddr, secret string) (string, error) {
	claims := JWTClaims{
		UserID:     userID,
		ETHAddress: ethAddr,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(sessionExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "dilithium-exchange",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ValidateJWT parses and validates a JWT, returning the claims.
func ValidateJWT(tokenStr, secret string) (*JWTClaims, error) {
	tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")
	token, err := jwt.ParseWithClaims(tokenStr, &JWTClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*JWTClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// NormalizeETHAddress lowercases an Ethereum address and ensures 0x prefix.
func NormalizeETHAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if !strings.HasPrefix(addr, "0x") {
		addr = "0x" + addr
	}
	return strings.ToLower(addr)
}

// IsValidETHAddress checks basic Ethereum address format.
func IsValidETHAddress(addr string) bool {
	addr = strings.TrimPrefix(strings.ToLower(addr), "0x")
	if len(addr) != 40 {
		return false
	}
	for _, c := range addr {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// IsValidDLTAddress checks basic DLT address format (40-char hex).
func IsValidDLTAddress(addr string) bool {
	if len(addr) != 40 {
		return false
	}
	for _, c := range strings.ToLower(addr) {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
