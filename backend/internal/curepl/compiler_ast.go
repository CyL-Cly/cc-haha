package curepl

import (
	"errors"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/javascriptparse"
)

// astView resolves the shared objects in Acorn's own-property graph. Shorthand
// properties can use the same Identifier as both key and value.
type astView struct {
	values map[int]*javascriptparse.Value
}
type syntaxNode struct {
	view  *astView
	value *javascriptparse.Value
}

func newAST(root *javascriptparse.Value) *astView {
	view := &astView{values: map[int]*javascriptparse.Value{}}
	var collect func(*javascriptparse.Value)
	collect = func(value *javascriptparse.Value) {
		if value == nil || value.Kind == "reference" {
			return
		}
		if value.ID != 0 {
			view.values[value.ID] = value
		}
		for _, property := range value.Properties {
			collect(property.Value)
		}
	}
	collect(root)
	return view
}
func (view *astView) resolve(value *javascriptparse.Value) *javascriptparse.Value {
	if value != nil && value.Kind == "reference" {
		return view.values[value.ID]
	}
	return value
}
func (view *astView) node(value *javascriptparse.Value) syntaxNode {
	return syntaxNode{view, view.resolve(value)}
}
func (node syntaxNode) get(name string) *javascriptparse.Value {
	if node.value == nil {
		return nil
	}
	for _, property := range node.value.Properties {
		if string(utf16.Decode(property.Key)) == name {
			return node.view.resolve(property.Value)
		}
	}
	return nil
}
func (node syntaxNode) child(name string) syntaxNode { return node.view.node(node.get(name)) }
func (node syntaxNode) string(name string) string {
	value := node.get(name)
	if value == nil {
		return ""
	}
	return string(utf16.Decode(value.Units))
}
func (node syntaxNode) kind() string { return node.string("type") }
func (node syntaxNode) valid() bool {
	return node.value != nil && node.value.Kind == "object" && node.get("type") != nil && node.get("type").Kind == "string"
}
func (node syntaxNode) integer(name string) int {
	value := node.get(name)
	if value == nil {
		return 0
	}
	number, _ := strconv.Atoi(value.Text)
	return number
}
func (node syntaxNode) boolean(name string) bool {
	value := node.get(name)
	return value != nil && value.Bool != nil && *value.Bool
}
func (node syntaxNode) same(other syntaxNode) bool {
	return node.value != nil && node.value == other.value
}
func (node syntaxNode) list(name string) []syntaxNode { return node.view.nodes(node.get(name)) }
func (view *astView) nodes(value *javascriptparse.Value) []syntaxNode {
	result := []syntaxNode{}
	value = view.resolve(value)
	if value == nil || value.Kind != "array" {
		return result
	}
	for _, property := range value.Properties {
		if _, err := strconv.Atoi(string(utf16.Decode(property.Key))); err != nil {
			continue
		}
		child := view.node(property.Value)
		if child.valid() {
			result = append(result, child)
		}
	}
	return result
}
func (node syntaxNode) children() []syntaxNode {
	result := []syntaxNode{}
	if node.value == nil {
		return result
	}
	for _, property := range node.value.Properties {
		value := node.view.resolve(property.Value)
		child := node.view.node(value)
		if child.valid() {
			result = append(result, child)
		} else if value != nil && value.Kind == "array" {
			result = append(result, node.view.nodes(value)...)
		}
	}
	return result
}

type astProperty struct {
	name  string
	value *javascriptparse.Value
}

func (node syntaxNode) properties() []astProperty {
	result := []astProperty{}
	if node.value != nil {
		for _, property := range node.value.Properties {
			result = append(result, astProperty{string(utf16.Decode(property.Key)), node.view.resolve(property.Value)})
		}
	}
	return result
}
func bindingNames(pattern syntaxNode) ([]string, error) {
	switch pattern.kind() {
	case "Identifier":
		return []string{pattern.string("name")}, nil
	case "RestElement":
		return bindingNames(pattern.child("argument"))
	case "AssignmentPattern":
		return bindingNames(pattern.child("left"))
	case "ArrayPattern":
		result := []string{}
		for _, element := range pattern.list("elements") {
			names, err := bindingNames(element)
			if err != nil {
				return nil, err
			}
			result = append(result, names...)
		}
		return result, nil
	case "ObjectPattern":
		result := []string{}
		for _, property := range pattern.list("properties") {
			key := "value"
			if property.kind() == "RestElement" {
				key = "argument"
			}
			names, err := bindingNames(property.child(key))
			if err != nil {
				return nil, err
			}
			result = append(result, names...)
		}
		return result, nil
	default:
		return nil, errors.New("Unsupported REPL binding pattern: " + pattern.kind())
	}
}
func assignmentNames(target syntaxNode) []string {
	switch target.kind() {
	case "MemberExpression":
		return []string{}
	case "ObjectPattern":
		result := []string{}
		for _, property := range target.list("properties") {
			key := "value"
			if property.kind() == "RestElement" {
				key = "argument"
			}
			result = append(result, assignmentNames(property.child(key))...)
		}
		return result
	case "ArrayPattern":
		result := []string{}
		for _, element := range target.list("elements") {
			result = append(result, assignmentNames(element)...)
		}
		return result
	case "AssignmentPattern":
		return assignmentNames(target.child("left"))
	case "RestElement":
		return assignmentNames(target.child("argument"))
	case "Identifier":
		return []string{target.string("name")}
	default:
		return []string{}
	}
}

type namesSet map[string]bool

func cloneNames(original namesSet) namesSet {
	result := namesSet{}
	for name, present := range original {
		result[name] = present
	}
	return result
}
func addPattern(names namesSet, pattern syntaxNode) error {
	bindings, err := bindingNames(pattern)
	if err != nil {
		return err
	}
	for _, name := range bindings {
		names[name] = true
	}
	return nil
}
func blockNames(body []syntaxNode, outer namesSet) (namesSet, error) {
	result := cloneNames(outer)
	for _, node := range body {
		if node.kind() == "VariableDeclaration" && node.string("kind") != "var" {
			for _, declaration := range node.list("declarations") {
				if err := addPattern(result, declaration.child("id")); err != nil {
					return nil, err
				}
			}
		} else if node.kind() == "FunctionDeclaration" || node.kind() == "ClassDeclaration" {
			if err := addPattern(result, node.child("id")); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}
