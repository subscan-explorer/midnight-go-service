package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBech32mEncodeDecode(t *testing.T) {
	// Test data
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	prefix := "tm" // testnet prefix

	// Test encoding
	encoded, err := Bech32mEncode(prefix, data)
	assert.NoError(t, err)
	assert.NotEmpty(t, encoded)
	assert.Contains(t, encoded, prefix)

	// Test decoding
	decodedPrefix, decodedData, err := Bech32mDecode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, prefix, decodedPrefix)
	assert.Equal(t, data, decodedData)
}

func TestHexToBech32m(t *testing.T) {
	// Test hex string
	hexStr := "0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	prefix := "tm"

	// Test conversion
	bech32mAddr, err := HexToBech32m(hexStr, prefix)
	assert.NoError(t, err)
	assert.NotEmpty(t, bech32mAddr)
	assert.Contains(t, bech32mAddr, prefix)
	// Decode to verify correctness
	hexPub, err := Bech32mToHex(bech32mAddr)
	assert.NoError(t, err)
	assert.Equal(t, hexStr, hexPub)
}

func TestBech32mToHex(t *testing.T) {
	// First create a valid Bech32m address
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	prefix := "tm"

	// Encode to Bech32m
	bech32mAddr, err := Bech32mEncode(prefix, data)
	assert.NoError(t, err)
	assert.NotEmpty(t, bech32mAddr)

	// Test conversion
	hexStr, err := Bech32mToHex(bech32mAddr)
	assert.NoError(t, err)
	assert.NotEmpty(t, hexStr)
	assert.Contains(t, hexStr, "0x")
}

func TestHexBech32mRoundTrip(t *testing.T) {
	// Test data - using a value without leading zeros for easier verification
	originalHex := "0x1234567890abcdef"
	prefix := "tm"

	// Convert hex to Bech32m
	bech32mAddr, err := HexToBech32m(originalHex, prefix)
	assert.NoError(t, err)
	assert.NotEmpty(t, bech32mAddr)

	// Convert back to hex
	convertedHex, err := Bech32mToHex(bech32mAddr)
	assert.NoError(t, err)
	assert.NotEmpty(t, convertedHex)

	// Verify round-trip
	// The converted hex should contain the original hex value (accounting for possible padding)
	assert.Contains(t, convertedHex, "1234567890abcdef")
}
func Test_AucAddress(t *testing.T) {
	bech32mAddr := "mn_shield-addr_test16wlc2a4jggr9jznaujdeylrgw5lqqucnzz0hcvahunnr4rm7hsnqxqzxckzp9fuyd0j2ryykg3kteaxjemlm0hy0ezjrsw5lxjazgzstx559z4w7"
	hexAddr, err := Bech32mToHex(bech32mAddr)
	assert.NoError(t, err)
	assert.Equal(t, "0xd3bf8576b24206590a7de49b927c68753e007313109f7c33b7e4e63a8f7ebc26030046c58412a7846be4a19096446cbcf4d2ceffb7dc8fc8a4383a9f34ba240a0b35", hexAddr)
	// Convert back to Bech32m
	convertedBech32mAddr, err := HexToBech32m(hexAddr, "mn_shield-addr_test")
	assert.NoError(t, err)
	assert.Equal(t, bech32mAddr, convertedBech32mAddr)
	hexAddr, err = Bech32mToHex("mn_addr_preview1lwpr7753ew696urrlgh5lmr0dan0u5k35xfzgwnvnksedvcnxyuqns0jhu")
	assert.NoError(t, err)

}
