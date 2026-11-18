package compile

import (
	"encoding/json"
	"fmt"
	"strings"
)

func parseStandardInputJSON(sourceCode string) (*StandardInputJson, error) {
	var stdInput StandardInputJson
	if err := json.Unmarshal([]byte(sourceCode), &stdInput); err != nil {
		return nil, fmt.Errorf("decode standard-input-json: %w", err)
	}

	if len(stdInput.Contracts) == 0 {
		return nil, fmt.Errorf("missing contracts")
	}

	stdInput.EntryFile = strings.TrimSpace(stdInput.EntryFile)
	if stdInput.EntryFile == "" {
		return nil, fmt.Errorf("missing entry-file")
	}

	if _, ok := stdInput.Contracts[stdInput.EntryFile]; !ok {
		return nil, fmt.Errorf("entry file %q not found in contracts", stdInput.EntryFile)
	}

	return &stdInput, nil
}
