package compile

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// StructField struct defines
// a field within a struct, including its name and type.
type StructField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// EnumDef enum define
type EnumDef struct {
	Members []string `json:"members"`
}

// StructDef is sorted by the fields order by array field
type StructDef struct {
	Fields []StructField `json:"fields"`
}

type ContractABI struct {
	Ledger  map[string]string    `json:"ledger"`
	Enums   map[string]EnumDef   `json:"enums"`
	Structs map[string]StructDef `json:"structs"`
}

type parseState int

const (
	stateDefault parseState = iota
	stateInEnum
	stateInStruct
)

var (
	// Match 'export ledger <name>: <type>;'
	reLedger = regexp.MustCompile(`^\s*export\s+ledger\s+([a-zA-Z0-9_]+)\s*:\s*([^;]+);`)
	// Match 'enum <Name> {'
	reEnumStart = regexp.MustCompile(`^\s*(?:export\s+)?enum\s+([a-zA-Z0-9_]+)\s*\{`)
	// Match 'struct <Name> {'
	reStructStart = regexp.MustCompile(`^\s*(?:export\s+)?struct\s+([a-zA-Z0-9_]+)\s*\{`)
	// Match '}'
	reBlockEnd = regexp.MustCompile(`^\s*\}\s*`)
	// Match enum
	reEnumMember = regexp.MustCompile(`^([a-zA-Z0-9_]+),?$`)
	// Match struct field
	reStructField = regexp.MustCompile(`^([a-zA-Z0-9_]+)\s*:\s*(.+)$`)
)

// ParseCompactContract parse contract content in compact format to ContractABI
func ParseCompactContract(sourceType SourceType, contractContent string) (*ContractABI, error) {
	abi := ContractABI{
		Ledger:  make(map[string]string),
		Enums:   make(map[string]EnumDef),
		Structs: make(map[string]StructDef),
	}

	currentState := stateDefault
	var currentEnumName string
	var currentStructName string
	var currentEnumMembers []string

	var currentStructFields []StructField

	if sourceType == MultiFile {
		standInput, err := parseStandardInputJSON(contractContent)
		if err != nil {
			return nil, err
		}
		flatContent := ""
		for _, content := range standInput.Contracts {
			flatContent = flatContent + "\n" + content
		}
		contractContent = flatContent
	}

	scanner := bufio.NewScanner(strings.NewReader(contractContent))

	for scanner.Scan() {
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)

		// 跳过空行
		if trimmedLine == "" {
			continue
		}

		switch currentState {
		case stateDefault:

			if matches := reLedger.FindStringSubmatch(line); len(matches) == 3 {
				fieldName := strings.TrimSpace(matches[1])
				fieldType := strings.TrimSpace(matches[2])
				abi.Ledger[fieldName] = fieldType
			} else if matches := reEnumStart.FindStringSubmatch(line); len(matches) == 2 {
				currentState = stateInEnum
				currentEnumName = matches[1]
				currentEnumMembers = []string{}
			} else if matches := reStructStart.FindStringSubmatch(line); len(matches) == 2 {
				currentState = stateInStruct
				currentStructName = matches[1]

				currentStructFields = []StructField{}
			}

		case stateInEnum:

			if reBlockEnd.MatchString(line) {

				abi.Enums[currentEnumName] = EnumDef{Members: currentEnumMembers}
				currentState = stateDefault
			} else if matches := reEnumMember.FindStringSubmatch(trimmedLine); len(matches) == 2 {

				currentEnumMembers = append(currentEnumMembers, matches[1])
			}

		case stateInStruct:
			if reBlockEnd.MatchString(line) {

				abi.Structs[currentStructName] = StructDef{Fields: currentStructFields}
				currentState = stateDefault // 返回默认状态
			} else if matches := reStructField.FindStringSubmatch(trimmedLine); len(matches) == 3 {
				fieldName := matches[1]
				fieldType := strings.TrimRight(strings.TrimSpace(matches[2]), ";")

				field := StructField{Name: fieldName, Type: fieldType}
				currentStructFields = append(currentStructFields, field)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// check final state
	if currentState != stateDefault {
		return nil, fmt.Errorf("unexpected end of file, parsing block was not finished (state: %d)", currentState)
	}
	if len(abi.Ledger) == 0 {
		return nil, nil
	}
	return &abi, nil
}
