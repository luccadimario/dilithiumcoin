package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// AddressToChecksummed converts a raw 40-char hex address to dlt1-prefixed checksummed format.
// Format: "dlt1" + 40-char hex + 4-char checksum = 48 chars total
func AddressToChecksummed(rawHex string) string {
	checksum := computeAddressChecksum(rawHex)
	return "dlt1" + rawHex + checksum
}

// computeAddressChecksum returns the first 4 hex chars of SHA256("dlt1" + address_hex)
func computeAddressChecksum(rawHex string) string {
	hash := sha256.Sum256([]byte("dlt1" + rawHex))
	return hex.EncodeToString(hash[:])[:4]
}

// NormalizeAddress accepts both old (40-char hex) and new (dlt1-prefixed 48-char) formats.
// Returns the raw 40-char hex address. For dlt1-prefixed addresses, validates the checksum.
// Other formats are passed through unchanged for backward compatibility.
func NormalizeAddress(address string) (string, error) {
	if len(address) == 48 && address[:4] == "dlt1" {
		rawHex := address[4:44]
		checksum := address[44:48]
		expected := computeAddressChecksum(rawHex)
		if checksum != expected {
			return "", fmt.Errorf("invalid address checksum: expected %s, got %s", expected, checksum)
		}
		return rawHex, nil
	}
	if len(address) == 40 {
		return address, nil
	}
	return address, nil
}
