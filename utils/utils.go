package utils

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"
)

// HexToBigInt converts a hex string to a big.Int
func HexToBigInt(hexStr string) (*big.Int, error) {
	// Remove "0x" prefix if present
	if len(hexStr) >= 2 && hexStr[:2] == "0x" {
		hexStr = hexStr[2:]
	}

	// Decode hex string
	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex string: %w", err)
	}

	// Convert to big.Int
	result := new(big.Int).SetBytes(bytes)
	return result, nil
}

// BigIntToHex converts a big.Int to a hex string
func BigIntToHex(value *big.Int) string {
	return fmt.Sprintf("0x%s", value.Text(16))
}

// ReverseBytes reverses the order of bytes in a slice
func ReverseBytes(data []byte) []byte {
	result := make([]byte, len(data))
	for i, b := range data {
		result[len(data)-1-i] = b
	}
	return result
}

// PadBytes pads a byte slice to a specific length
func PadBytes(data []byte, length int) []byte {
	if len(data) >= length {
		return data[:length]
	}

	result := make([]byte, length)
	copy(result[length-len(data):], data)
	return result
}

// Uint64ToBytes converts a uint64 to bytes
func Uint64ToBytes(value uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, value)
	return buf
}

// BytesToUint64 converts bytes to a uint64
func BytesToUint64(data []byte) uint64 {
	if len(data) < 8 {
		// Pad with zeros if needed
		padded := make([]byte, 8)
		copy(padded[8-len(data):], data)
		return binary.BigEndian.Uint64(padded)
	}
	return binary.BigEndian.Uint64(data[:8])
}

// ConcatBytes concatenates multiple byte slices
func ConcatBytes(slices ...[]byte) []byte {
	var result []byte
	for _, slice := range slices {
		result = append(result, slice...)
	}
	return result
}

// CloneBytes creates a deep copy of a byte slice
func CloneBytes(data []byte) []byte {
	result := make([]byte, len(data))
	copy(result, data)
	return result
}

// CompareBytes compares two byte slices
func CompareBytes(a, b []byte) int {
	return bytes.Compare(a, b)
}

// IsZeroBytes checks if all bytes in a slice are zero
func IsZeroBytes(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}

// XorBytes performs XOR operation on two byte slices
func XorBytes(a, b []byte) []byte {
	if len(a) != len(b) {
		panic("byte slices must have the same length")
	}

	result := make([]byte, len(a))
	for i := range a {
		result[i] = a[i] ^ b[i]
	}
	return result
}
func HexToBytes(s string) []byte {
	s = strings.TrimPrefix(s, "0x")
	c := make([]byte, hex.DecodedLen(len(s)))
	_, _ = hex.Decode(c, []byte(s))
	return c
}

func BytesToHex(b []byte) string {
	c := make([]byte, hex.EncodedLen(len(b)))
	hex.Encode(c, b)
	return string(c)
}

// Add `0x` prefix
func AddHex(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	if strings.HasPrefix(s, "0x") {
		return s
	}
	return strings.ToLower("0x" + s)
}

func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		value = defaultValue
	}
	return value
}

func TrimHex(s string) string {
	return strings.TrimPrefix(s, "0x")
}

func ConvertWithNetworkIdAccount(account string) string {
	account = strings.TrimPrefix(account, "0x")
	if account == "" {
		return ""
	}
	if len(account) == 70 {
		account = account[2:] // remove network id prefix
	}
	return "0x" + account
}
