package powershellpolicy

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf16"
)

var cmdletName = regexp.MustCompile(`^[A-Za-z]+-[A-Za-z][A-Za-z0-9_]*$`)
var driveName = regexp.MustCompile(`^[A-Za-z]:`)

func classifyCommandName(name string) string {
	if cmdletName.MatchString(name) {
		return "cmdlet"
	}
	if strings.ContainsAny(name, `.\/`) {
		return "application"
	}
	return "unknown"
}
func StripModulePrefix(name string) string {
	index := strings.LastIndex(name, `\`)
	if index < 0 || driveName.MatchString(name) || strings.HasPrefix(name, `\\`) || strings.HasPrefix(name, `.\`) || strings.HasPrefix(name, `..\`) {
		return name
	}
	return name[index+1:]
}
func mapElementType(kind, expression string) string {
	switch kind {
	case "ScriptBlockExpressionAst":
		return "ScriptBlock"
	case "SubExpressionAst", "ArrayExpressionAst", "ParenExpressionAst":
		return "SubExpression"
	case "ExpandableStringExpressionAst":
		return "ExpandableString"
	case "InvokeMemberExpressionAst", "MemberExpressionAst":
		return "MemberInvocation"
	case "VariableExpressionAst":
		return "Variable"
	case "StringConstantExpressionAst", "ConstantExpressionAst":
		return "StringConstant"
	case "CommandParameterAst":
		return "Parameter"
	case "CommandExpressionAst":
		if expression != "" {
			return mapElementType(expression, "")
		}
	}
	return "Other"
}
func mapStatementType(kind string) string {
	switch kind {
	case "PipelineAst", "PipelineChainAst", "AssignmentStatementAst", "IfStatementAst", "ForStatementAst", "ForEachStatementAst", "WhileStatementAst", "DoWhileStatementAst", "DoUntilStatementAst", "SwitchStatementAst", "TryStatementAst", "TrapStatementAst", "FunctionDefinitionAst", "DataStatementAst":
		return kind
	}
	return "UnknownStatementAst"
}
func transformRedirection(raw rawRedirection) Redirection {
	if raw.Type == "MergingRedirectionAst" {
		return Redirection{Operator: "2>&1", Target: "", IsMerging: true}
	}
	operator := ">"
	if raw.Append {
		operator = ">>"
	}
	if raw.FromStream == "Error" {
		operator = "2" + operator
	} else if raw.FromStream == "All" {
		operator = "*" + operator
	}
	return Redirection{Operator: operator, Target: raw.LocationText}
}
func transformCommand(raw rawCommand) Command {
	if raw.Type != "CommandAst" {
		kind := "CommandExpressionAst"
		if raw.Type == "ParenExpressionAst" {
			kind = raw.Type
		}
		return Command{Name: raw.Text, NameType: "unknown", ElementType: kind, Args: []string{}, Text: raw.Text, ElementTypes: []string{mapElementType(raw.Type, raw.ExpressionType)}}
	}
	command := Command{NameType: "unknown", ElementType: "CommandAst", Args: []string{}, Text: raw.Text, ElementTypes: []string{}}
	children := [][]Child{}
	hasChildren := false
	for i, element := range raw.CommandElements {
		stringLiteral := element.Type == "StringConstantExpressionAst" || element.Type == "ExpandableStringExpressionAst"
		value := element.Text
		var resolved string
		if stringLiteral && len(element.Value) > 0 && json.Unmarshal(element.Value, &resolved) == nil && string(element.Value) != "null" {
			value = resolved
		}
		if i == 0 {
			if strings.HasPrefix(value, "'") || strings.HasPrefix(value, `"`) {
				value = value[1:]
			}
			if strings.HasSuffix(value, "'") || strings.HasSuffix(value, `"`) {
				value = value[:len(value)-1]
			}
			command.NameType = classifyCommandName(value)
			for _, unit := range utf16.Encode([]rune(value)) {
				if unit >= 0x80 {
					command.NameType = "application"
					break
				}
			}
			command.Name = StripModulePrefix(value)
		} else {
			command.Args = append(command.Args, value)
			var nodes []Child
			for _, child := range element.Children {
				nodes = append(nodes, Child{Type: mapElementType(child.Type, ""), Text: child.Text})
			}
			if len(nodes) > 0 {
				hasChildren = true
			}
			children = append(children, nodes)
		}
		command.ElementTypes = append(command.ElementTypes, mapElementType(element.Type, element.ExpressionType))
	}
	if hasChildren {
		command.Children = children
	}
	for _, r := range raw.Redirections {
		command.Redirections = append(command.Redirections, transformRedirection(r))
	}
	return command
}
func transformStatement(raw rawStatement) Statement {
	statement := Statement{StatementType: mapStatementType(raw.Type), Commands: []Command{}, Redirections: []Redirection{}, Text: raw.Text, SecurityPatterns: raw.SecurityPatterns}
	if raw.Elements != nil {
		for _, element := range *raw.Elements {
			statement.Commands = append(statement.Commands, transformCommand(element))
			for _, r := range element.Redirections {
				statement.Redirections = append(statement.Redirections, transformRedirection(r))
			}
		}
		seen := map[string]bool{}
		for _, r := range statement.Redirections {
			seen[r.Operator+"\x00"+r.Target] = true
		}
		for _, rawR := range raw.Redirections {
			r := transformRedirection(rawR)
			key := r.Operator + "\x00" + r.Target
			if !seen[key] {
				seen[key] = true
				statement.Redirections = append(statement.Redirections, r)
			}
		}
	} else {
		statement.Commands = append(statement.Commands, Command{Name: raw.Text, NameType: "unknown", ElementType: "CommandExpressionAst", Args: []string{}, Text: raw.Text})
		for _, r := range raw.Redirections {
			statement.Redirections = append(statement.Redirections, transformRedirection(r))
		}
	}
	for _, command := range raw.NestedCommands {
		statement.NestedCommands = append(statement.NestedCommands, transformCommand(command))
	}
	return statement
}
func TransformRaw(data []byte) (Parsed, error) {
	if jsTrim(string(data)) == "null" {
		return Parsed{}, errors.New("native AST output must be an object")
	}
	var raw rawParsed
	if err := json.Unmarshal(data, &raw); err != nil {
		return Parsed{}, err
	}
	parsed := Parsed{Valid: raw.Valid, Errors: append([]ParseError{}, raw.Errors...), Statements: []Statement{}, Variables: append([]Variable{}, raw.Variables...), HasStopParsing: raw.HasStopParsing, OriginalCommand: raw.OriginalCommand, TypeLiterals: raw.TypeLiterals, HasUsingStatements: raw.HasUsingStatements, HasScriptRequirements: raw.HasScriptRequirements}
	for _, statement := range raw.Statements {
		parsed.Statements = append(parsed.Statements, transformStatement(statement))
	}
	return parsed, nil
}
