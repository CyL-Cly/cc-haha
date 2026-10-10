// Package curepl contains the native Computer Use REPL compiler and worker.
// This compiler runs Go AST transformations; it never evaluates a cell or
// invokes the original TypeScript compiler as a production fallback.
package curepl

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/javascriptparse"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Binding struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type CompiledCell struct {
	Bindings []Binding `json:"bindings"`
	Warnings []string  `json:"warnings"`
	Source   []uint16  `json:"-"`
}
type CompilerOptions struct{ RandomUUID func() (string, error) }
type sourceEdit struct {
	start, end int
	text       []uint16
}
type insertion struct {
	position int
	text     string
}
type hoistedDeclaration struct{ node, parent syntaxNode }

func privateUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 15) | 64
	value[8] = (value[8] & 63) | 128
	hexadecimal := hex.EncodeToString(value[:])
	return hexadecimal[:8] + "-" + hexadecimal[8:12] + "-" + hexadecimal[12:16] + "-" + hexadecimal[16:20] + "-" + hexadecimal[20:], nil
}
func units(value string) []uint16 { return utf16.Encode([]rune(value)) }
func quote(value string) string   { return string(protocol.QuoteJavaScriptUTF16(units(value))) }
func quotedNames(names []string) string {
	quoted := []string{}
	for _, name := range names {
		quoted = append(quoted, quote(name))
	}
	return strings.Join(quoted, ",")
}

// CompileAST consumes an AST from strict Acorn {latest,module} parsing. Its
// offsets and returned source are UTF-16, including original lone surrogates.
func CompileAST(code []uint16, ast *javascriptparse.Value, prior []Binding, options CompilerOptions) (CompiledCell, error) {
	view := newAST(ast)
	root := view.node(ast)
	if root.kind() != "Program" {
		return CompiledCell{}, errors.New("REPL compiler requires a parsed Program")
	}
	current := map[string]Binding{}
	order := []string{}
	identifiers := namesSet{}
	hoisted := []hoistedDeclaration{}
	insertions := []insertion{}
	put := func(name, kind string) {
		if _, exists := current[name]; !exists {
			order = append(order, name)
		}
		current[name] = Binding{name, kind}
	}
	var inspect func(syntaxNode, int, bool, syntaxNode) error
	inspect = func(node syntaxNode, depth int, inFunctionScope bool, parent syntaxNode) error {
		kind := node.kind()
		variableStatement := kind == "VariableDeclaration" && !(parent.kind() == "ForStatement" && parent.child("init").same(node)) && !((parent.kind() == "ForOfStatement" || parent.kind() == "ForInStatement") && parent.child("left").same(node))
		if variableStatement || kind == "ExpressionStatement" || kind == "ReturnStatement" || kind == "ThrowStatement" {
			end := node.integer("end")
			if end > 0 && end <= len(code) && code[end-1] != ';' {
				insertions = append(insertions, insertion{end, ";"})
			}
		}
		if kind == "Identifier" {
			identifiers[node.string("name")] = true
		}
		importMeta := kind == "MetaProperty" && node.child("meta").string("name") == "import"
		if strings.HasPrefix(kind, "Import") || strings.HasPrefix(kind, "Export") || importMeta {
			return errors.New("Module imports and exports are not available in Computer Use JavaScript")
		}
		if kind == "VariableDeclaration" && !inFunctionScope && (depth == 1 || node.string("kind") == "var") {
			for _, declaration := range node.list("declarations") {
				names, err := bindingNames(declaration.child("id"))
				if err != nil {
					return err
				}
				for _, name := range names {
					put(name, node.string("kind"))
				}
			}
			if node.string("kind") == "var" {
				hoisted = append(hoisted, hoistedDeclaration{node, parent})
			}
		}
		if depth == 1 && (kind == "FunctionDeclaration" || kind == "ClassDeclaration") {
			name := node.child("id").string("name")
			bindingKind := "class"
			if kind == "FunctionDeclaration" {
				bindingKind = "function"
				hoisted = append(hoisted, hoistedDeclaration{node, parent})
			}
			put(name, bindingKind)
		}
		nested := inFunctionScope || strings.Contains(kind, "Function") || kind == "StaticBlock"
		for _, child := range node.children() {
			if err := inspect(child, depth+1, nested, node); err != nil {
				return err
			}
		}
		return nil
	}
	if err := inspect(root, 0, false, syntaxNode{}); err != nil {
		return CompiledCell{}, err
	}
	carried := []Binding{}
	constants := namesSet{}
	for _, binding := range prior {
		if _, exists := current[binding.Name]; !exists {
			carried = append(carried, binding)
			if binding.Kind == "const" {
				constants[binding.Name] = true
			}
		}
	}
	reassigned, err := reassignedPriorConstants(root, constants)
	if err != nil {
		return CompiledCell{}, err
	}
	warned := append([]string{}, reassigned...)
	warnedSet := namesSet{}
	for _, name := range warned {
		warnedSet[name] = true
	}
	for _, name := range order {
		binding := current[name]
		if binding.Kind != "const" {
			continue
		}
		for _, previous := range prior {
			if previous.Name == name && previous.Kind == "const" && !warnedSet[name] {
				warned = append(warned, name)
				warnedSet[name] = true
				break
			}
		}
	}
	bindings := append([]Binding{}, carried...)
	for _, name := range order {
		bindings = append(bindings, current[name])
	}
	if options.RandomUUID == nil {
		options.RandomUUID = privateUUID
	}
	prefix := ""
	for {
		value, err := options.RandomUUID()
		if err != nil {
			return CompiledCell{}, err
		}
		prefix = "__cu_cell_" + strings.ReplaceAll(value, "-", "_")
		collision := false
		for _, suffix := range []string{"_scopeFactory", "_scope", "_register", "_mark", "_value"} {
			if identifiers[prefix+suffix] {
				collision = true
				break
			}
		}
		if !collision {
			break
		}
	}
	scopeFactory, scope, register, mark, value := prefix+"_scopeFactory", prefix+"_scope", prefix+"_register", prefix+"_mark", prefix+"_value"
	registrations := []string{}
	for _, name := range order {
		binding := current[name]
		hoistedValue := binding.Kind == "var" || binding.Kind == "function"
		registrations = append(registrations, "["+quote(name)+", "+quote(binding.Kind)+", () => "+name+", "+value+" => { "+name+" = "+value+" }, "+strconv.FormatBool(hoistedValue)+"]")
	}
	markerCounter := 0
	for _, item := range hoisted {
		node, parent := item.node, item.parent
		if node.kind() == "FunctionDeclaration" {
			insertions = append(insertions, insertion{node.integer("end"), ";" + mark + "(" + quote(node.child("id").string("name")) + ");"})
			continue
		}
		iteration := (parent.kind() == "ForOfStatement" || parent.kind() == "ForInStatement") && parent.child("left").same(node)
		if iteration {
			names := []string{}
			for _, declaration := range node.list("declarations") {
				binding, err := bindingNames(declaration.child("id"))
				if err != nil {
					return CompiledCell{}, err
				}
				names = append(names, binding...)
			}
			marker := mark + "(" + quotedNames(names) + ");"
			body := parent.child("body")
			if body.kind() == "BlockStatement" {
				insertions = append(insertions, insertion{body.integer("start") + 1, marker})
			} else {
				insertions = append(insertions, insertion{body.integer("start"), "{" + marker}, insertion{body.integer("end"), "}"})
			}
			continue
		}
		for _, declaration := range node.list("declarations") {
			names, err := bindingNames(declaration.child("id"))
			if err != nil {
				return CompiledCell{}, err
			}
			insertions = append(insertions, insertion{declaration.integer("end"), ", " + prefix + "_reached_" + strconv.Itoa(markerCounter) + " = " + mark + "(" + quotedNames(names) + ")"})
			markerCounter++
		}
	}
	futureVars := map[string]int{}
	for _, item := range hoisted {
		if item.node.kind() != "VariableDeclaration" {
			continue
		}
		for _, declaration := range item.node.list("declarations") {
			names, err := bindingNames(declaration.child("id"))
			if err != nil {
				return CompiledCell{}, err
			}
			for _, name := range names {
				position := declaration.integer("start")
				previous, exists := futureVars[name]
				if !exists || position < previous {
					futureVars[name] = position
				}
			}
		}
	}
	for _, statement := range root.list("body") {
		if statement.kind() != "ExpressionStatement" {
			continue
		}
		expression := statement.child("expression")
		target := syntaxNode{}
		if expression.kind() == "AssignmentExpression" {
			target = expression.child("left")
		} else if expression.kind() == "UpdateExpression" {
			target = expression.child("argument")
		}
		if !target.valid() {
			continue
		}
		names := []string{}
		for _, name := range assignmentNames(target) {
			if position, exists := futureVars[name]; exists && position > expression.integer("start") {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}
		marker := mark + "(" + quotedNames(names) + ")"
		operator := expression.string("operator")
		if operator == "&&=" || operator == "||=" || operator == "??=" {
			right := expression.child("right")
			insertions = append(insertions, insertion{right.integer("start"), "((" + value + ") => (" + marker + ", " + value + "))("}, insertion{right.integer("end"), ")"})
		} else {
			insertions = append(insertions, insertion{statement.integer("end"), ";" + marker + ";"})
		}
	}
	edits, err := persistentReferences(root, scope)
	if err != nil {
		return CompiledCell{}, err
	}
	for _, insertion := range insertions {
		edits = append(edits, sourceEdit{insertion.position, insertion.position, units(insertion.text)})
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start > edits[j].start
		}
		return edits[i].end > edits[j].end
	})
	instrumented := append([]uint16{}, code...)
	for _, edit := range edits {
		if edit.start < 0 || edit.end < edit.start || edit.end > len(instrumented) {
			return CompiledCell{}, errors.New("Invalid REPL AST source edit")
		}
		next := make([]uint16, 0, len(instrumented)+len(edit.text)-(edit.end-edit.start))
		next = append(next, instrumented[:edit.start]...)
		next = append(next, edit.text...)
		next = append(next, instrumented[edit.end:]...)
		instrumented = next
	}
	opening := "(async (" + scopeFactory + ", " + register + ", " + mark + ") => {\n\"use strict\";\nconst " + scope + " = " + scopeFactory + "([" + quotedNames(reassigned) + "]);\n" + register + "([" + strings.Join(registrations, ",\n") + "]);\n"
	source := append(units(opening), instrumented...)
	source = append(source, units("\n})")...)
	warnings := []string{}
	for _, name := range warned {
		warnings = append(warnings, name+" was declared with const; use let for reassignable variables.")
	}
	return CompiledCell{bindings, warnings, source}, nil
}
