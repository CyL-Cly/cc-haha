// Package powershellpolicy consumes the actual PowerShell native AST instead
// of interpreting PowerShell syntax with a shell tokenizer. Its contracts are
// the frozen MAIN PowerShellTool parser, classification and permission policy.
package powershellpolicy

import "encoding/json"

type ParseError struct {
	Message string `json:"message"`
	ErrorID string `json:"errorId"`
}
type Child struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type Command struct {
	Name         string        `json:"name"`
	NameType     string        `json:"nameType"`
	ElementType  string        `json:"elementType"`
	Args         []string      `json:"args"`
	Text         string        `json:"text"`
	ElementTypes []string      `json:"elementTypes,omitempty"`
	Children     [][]Child     `json:"children,omitempty"`
	Redirections []Redirection `json:"redirections,omitempty"`
}
type Redirection struct {
	Operator  string `json:"operator"`
	Target    string `json:"target"`
	IsMerging bool   `json:"isMerging"`
}
type Patterns struct {
	HasMemberInvocations bool `json:"hasMemberInvocations,omitempty"`
	HasSubExpressions    bool `json:"hasSubExpressions,omitempty"`
	HasExpandableStrings bool `json:"hasExpandableStrings,omitempty"`
	HasScriptBlocks      bool `json:"hasScriptBlocks,omitempty"`
}
type Statement struct {
	StatementType    string        `json:"statementType"`
	Commands         []Command     `json:"commands"`
	Redirections     []Redirection `json:"redirections"`
	Text             string        `json:"text"`
	NestedCommands   []Command     `json:"nestedCommands,omitempty"`
	SecurityPatterns *Patterns     `json:"securityPatterns,omitempty"`
}
type Variable struct {
	Path       string `json:"path"`
	IsSplatted bool   `json:"isSplatted"`
}
type Parsed struct {
	Valid                 bool         `json:"valid"`
	Errors                []ParseError `json:"errors"`
	Statements            []Statement  `json:"statements"`
	Variables             []Variable   `json:"variables"`
	HasStopParsing        bool         `json:"hasStopParsing"`
	OriginalCommand       string       `json:"originalCommand"`
	TypeLiterals          []string     `json:"typeLiterals,omitempty"`
	HasUsingStatements    bool         `json:"hasUsingStatements,omitempty"`
	HasScriptRequirements bool         `json:"hasScriptRequirements,omitempty"`
}
type SecurityFlags struct {
	HasSubExpressions    bool `json:"hasSubExpressions"`
	HasScriptBlocks      bool `json:"hasScriptBlocks"`
	HasSplatting         bool `json:"hasSplatting"`
	HasExpandableStrings bool `json:"hasExpandableStrings"`
	HasMemberInvocations bool `json:"hasMemberInvocations"`
	HasAssignments       bool `json:"hasAssignments"`
	HasStopParsing       bool `json:"hasStopParsing"`
}

// PowerShell 5.1 ConvertTo-Json may unwrap a singleton. Accept precisely its
// array-or-one contract while retaining syntax/type errors as invalid output.
type arrayOrOne[T any] []T

func (s *arrayOrOne[T]) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*s = []T{}
		return nil
	}
	if len(b) > 0 && b[0] == '[' {
		return json.Unmarshal(b, (*[]T)(s))
	}
	var value T
	if err := json.Unmarshal(b, &value); err != nil {
		return err
	}
	*s = []T{value}
	return nil
}

type rawElement struct {
	Type           string            `json:"type"`
	Text           string            `json:"text"`
	Value          json.RawMessage   `json:"value"`
	ExpressionType string            `json:"expressionType"`
	Children       arrayOrOne[Child] `json:"children"`
}
type rawRedirection struct {
	Type         string `json:"type"`
	Append       bool   `json:"append"`
	FromStream   string `json:"fromStream"`
	LocationText string `json:"locationText"`
}
type rawCommand struct {
	Type            string                     `json:"type"`
	Text            string                     `json:"text"`
	CommandElements arrayOrOne[rawElement]     `json:"commandElements"`
	Redirections    arrayOrOne[rawRedirection] `json:"redirections"`
	ExpressionType  string                     `json:"expressionType"`
}
type rawStatement struct {
	Type             string                     `json:"type"`
	Text             string                     `json:"text"`
	Elements         *arrayOrOne[rawCommand]    `json:"elements"`
	NestedCommands   arrayOrOne[rawCommand]     `json:"nestedCommands"`
	Redirections     arrayOrOne[rawRedirection] `json:"redirections"`
	SecurityPatterns *Patterns                  `json:"securityPatterns"`
}
type rawParsed struct {
	Valid                 bool                     `json:"valid"`
	Errors                arrayOrOne[ParseError]   `json:"errors"`
	Statements            arrayOrOne[rawStatement] `json:"statements"`
	Variables             arrayOrOne[Variable]     `json:"variables"`
	HasStopParsing        bool                     `json:"hasStopParsing"`
	OriginalCommand       string                   `json:"originalCommand"`
	TypeLiterals          arrayOrOne[string]       `json:"typeLiterals"`
	HasUsingStatements    bool                     `json:"hasUsingStatements"`
	HasScriptRequirements bool                     `json:"hasScriptRequirements"`
}
