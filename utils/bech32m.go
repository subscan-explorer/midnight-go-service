package utils

import (
	"fmt"
	"github.com/btcsuite/btcd/btcutil/bech32"
)

// Bech32mEncode encodes a byte array to a Bech32m address
func Bech32mEncode(prefix string, data []byte) (string, error) {
	// Convert data to base32 (5-bit groups)
	fiveBitData, err := bech32.ConvertBits(data, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("failed to convert bits: %w", err)
	}

	// Encode using Bech32m
	encoded, err := bech32.EncodeM(prefix, fiveBitData)
	if err != nil {
		return "", fmt.Errorf("failed to encode Bech32m: %w", err)
	}

	return encoded, nil
}

// Bech32mDecode decodes a Bech32m address to a byte array
func Bech32mDecode(encoded string) (prefix string, data []byte, err error) {
	// Decode Bech32m string
	prefix, fiveBitData, err := bech32.DecodeNoLimit(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("failed to decode Bech32m: %w", err)
	}

	// Convert from base32 (5-bit groups) to bytes (8-bit groups)
	data, err = bech32.ConvertBits(fiveBitData, 5, 8, false)
	if err != nil {
		return "", nil, fmt.Errorf("failed to convert bits: %w", err)
	}

	return prefix, data, nil
}

// HexToBech32m converts a hex string to a Bech32m address
func HexToBech32m(hexStr, prefix string) (string, error) {
	// Convert to byte array
	byteArray := HexToBytes(hexStr)
	fmt.Println("Hex to byte array:", len(byteArray))
	// Ensure the byte array is 32 bytes (pad with leading zeros if needed)
	if len(byteArray) < 32 {
		padded := make([]byte, 32)
		copy(padded[32-len(byteArray):], byteArray)
		byteArray = padded
	}
	// Encode to Bech32m
	return Bech32mEncode(prefix, byteArray)
}

// Bech32mToHex converts a Bech32m address to a hex string
func Bech32mToHex(bech32mAddr string) (string, error) {
	// Decode Bech32m
	_, data, err := Bech32mDecode(bech32mAddr)
	if err != nil {
		return "", fmt.Errorf("failed to decode Bech32m: %w", err)
	}

	// Convert to hex
	hexStr := BytesToHex(data)
	return AddHex(hexStr), nil
}
