package compile

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// CMAKey preserves the protocol's key type; legacy strings have no known type.
// Unknown future tags are retained without interpreting their cryptography.
type CMAKey struct {
	Tag   string `json:"tag"`
	Value string `json:"value"`
}

// CMACommittee reads both Ledger 8 string keys and Ledger 9 tagged keys.
type CMACommittee []CMAKey

// UnmarshalJSON normalizes legacy keys while rejecting corrupt state responses.
func (c *CMACommittee) UnmarshalJSON(data []byte) error {
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("decode CMA committee: %w", err)
	}
	if entries == nil {
		*c = nil
		return nil
	}
	result := make(CMACommittee, 0, len(entries))
	for i, entry := range entries {
		var key CMAKey
		if bytes.HasPrefix(bytes.TrimSpace(entry), []byte(`"`)) {
			key.Tag = "legacy"
			if err := json.Unmarshal(entry, &key.Value); err != nil {
				return fmt.Errorf("decode legacy CMA key %d: %w", i, err)
			}
		} else {
			if err := json.Unmarshal(entry, &key); err != nil {
				return fmt.Errorf("decode typed CMA key %d: %w", i, err)
			}
		}
		if key.Tag == "" || key.Value == "" {
			return fmt.Errorf("invalid CMA key %d: tag and value required", i)
		}
		result = append(result, key)
	}
	*c = result
	return nil
}

// Values preserves the legacy CMA endpoint's array-of-strings response.
func (c CMACommittee) Values() []string {
	if c == nil {
		return nil
	}
	values := make([]string, len(c))
	for i, key := range c {
		values[i] = key.Value
	}
	return values
}
