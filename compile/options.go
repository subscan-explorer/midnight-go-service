package compile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"midnight-go-sdk/config"
)

// CompilerOptions is the strictly typed set of supported compilation switches.
type CompilerOptions struct {
	FeatureZKIRV3 bool `json:"feature_zkir_v3"`
}

// UnmarshalJSON rejects unrecognized flags, including shell argument fields.
func (o *CompilerOptions) UnmarshalJSON(data []byte) error {
	type wire CompilerOptions
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode compiler options: %w", err)
	}
	*o = CompilerOptions(value)
	return nil
}

// ValidateCompilerOptions preserves historical verification and isolates Ledger 9 compilers to compatible networks.
func ValidateCompilerOptions(networkID config.NetworkID, version string, options CompilerOptions) error {
	supported := false
	for _, candidate := range CompactVersions {
		if candidate == version {
			supported = true
		}
	}
	if !supported || (version == "0.35.0" && networkID != config.Stagenet) ||
		(version == "0.36.0" && networkID != config.MainNet && networkID != config.Preview) {
		return fmt.Errorf("unsupported compiler %s for %s", version, networkID)
	}
	if options.FeatureZKIRV3 && (version != "0.35.0" || networkID != config.Stagenet) {
		return fmt.Errorf("feature_zkir_v3 requires Stagenet compiler 0.35.0")
	}
	return nil
}
