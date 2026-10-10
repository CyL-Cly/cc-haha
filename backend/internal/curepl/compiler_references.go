package curepl

import "strings"

func collectFunctionVars(node syntaxNode, target namesSet) error {
	if strings.Contains(node.kind(), "Function") || node.kind() == "StaticBlock" {
		return nil
	}
	if node.kind() == "VariableDeclaration" && node.string("kind") == "var" {
		for _, declaration := range node.list("declarations") {
			if err := addPattern(target, declaration.child("id")); err != nil {
				return err
			}
		}
	}
	for _, child := range node.children() {
		if err := collectFunctionVars(child, target); err != nil {
			return err
		}
	}
	return nil
}
func reassignedPriorConstants(ast syntaxNode, names namesSet) ([]string, error) {
	assigned := []string{}
	seen := namesSet{}
	var visit func(syntaxNode, namesSet) error
	visit = func(node syntaxNode, outer namesSet) error {
		shadowed := cloneNames(outer)
		kind := node.kind()
		if kind == "BlockStatement" {
			var err error
			shadowed, err = blockNames(node.list("body"), outer)
			if err != nil {
				return err
			}
		}
		if strings.Contains(kind, "Function") {
			if id := node.child("id"); id.valid() {
				if err := addPattern(shadowed, id); err != nil {
					return err
				}
			}
			for _, parameter := range node.list("params") {
				if err := addPattern(shadowed, parameter); err != nil {
					return err
				}
			}
			if err := collectFunctionVars(node.child("body"), shadowed); err != nil {
				return err
			}
		}
		if kind == "CatchClause" {
			if parameter := node.child("param"); parameter.valid() {
				if err := addPattern(shadowed, parameter); err != nil {
					return err
				}
			}
		}
		if strings.HasPrefix(kind, "For") {
			declaration := node.child("init")
			if !declaration.valid() {
				declaration = node.child("left")
			}
			if declaration.valid() && declaration.kind() == "VariableDeclaration" && declaration.string("kind") != "var" {
				for _, item := range declaration.list("declarations") {
					if err := addPattern(shadowed, item.child("id")); err != nil {
						return err
					}
				}
			}
		}
		target := syntaxNode{}
		if kind == "AssignmentExpression" {
			target = node.child("left")
		}
		if kind == "UpdateExpression" {
			target = node.child("argument")
		}
		if (kind == "ForOfStatement" || kind == "ForInStatement") && node.child("left").kind() != "VariableDeclaration" {
			target = node.child("left")
		}
		if target.valid() {
			for _, name := range assignmentNames(target) {
				if names[name] && !shadowed[name] && !seen[name] {
					assigned = append(assigned, name)
					seen[name] = true
				}
			}
		}
		for _, child := range node.children() {
			if err := visit(child, shadowed); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(ast, namesSet{}); err != nil {
		return nil, err
	}
	return assigned, nil
}

func persistentReferences(ast syntaxNode, scope string) ([]sourceEdit, error) {
	edits := []sourceEdit{}
	var visit func(syntaxNode, namesSet, syntaxNode, string, string) error
	var patternExpressions func(syntaxNode, namesSet) error
	patternExpressions = func(pattern syntaxNode, shadowed namesSet) error {
		switch pattern.kind() {
		case "AssignmentPattern":
			if err := patternExpressions(pattern.child("left"), shadowed); err != nil {
				return err
			}
			return visit(pattern.child("right"), shadowed, syntaxNode{}, "", "")
		case "ObjectPattern":
			for _, property := range pattern.list("properties") {
				if property.kind() == "RestElement" {
					if err := patternExpressions(property.child("argument"), shadowed); err != nil {
						return err
					}
				} else {
					if property.boolean("computed") {
						if err := visit(property.child("key"), shadowed, syntaxNode{}, "", ""); err != nil {
							return err
						}
					}
					if err := patternExpressions(property.child("value"), shadowed); err != nil {
						return err
					}
				}
			}
		case "ArrayPattern":
			for _, element := range pattern.list("elements") {
				if err := patternExpressions(element, shadowed); err != nil {
					return err
				}
			}
		case "RestElement":
			return patternExpressions(pattern.child("argument"), shadowed)
		}
		return nil
	}
	visit = func(node syntaxNode, outer namesSet, parent syntaxNode, key, shorthand string) error {
		kind := node.kind()
		if kind == "UnaryExpression" && node.string("operator") == "typeof" && node.child("argument").kind() == "Identifier" {
			name := node.child("argument").string("name")
			if !outer[name] {
				edits = append(edits, sourceEdit{node.integer("start"), node.integer("end"), units(scope + ".typeOf(" + quote(name) + ")")})
				return nil
			}
		}
		if kind == "Identifier" {
			name := node.string("name")
			if outer[name] {
				return nil
			}
			text := scope + ".values[" + quote(name) + "]"
			if parent.kind() == "CallExpression" && key == "callee" || parent.kind() == "TaggedTemplateExpression" && key == "tag" {
				text = "(0, " + text + ")"
			}
			if shorthand != "" {
				text = shorthand + ": " + text
			}
			edits = append(edits, sourceEdit{node.integer("start"), node.integer("end"), units(text)})
			return nil
		}
		if strings.Contains(kind, "Function") {
			parameters := cloneNames(outer)
			if kind != "ArrowFunctionExpression" {
				parameters["arguments"] = true
			}
			if kind == "FunctionExpression" && node.child("id").valid() {
				if err := addPattern(parameters, node.child("id")); err != nil {
					return err
				}
			}
			for _, parameter := range node.list("params") {
				if err := addPattern(parameters, parameter); err != nil {
					return err
				}
			}
			for _, parameter := range node.list("params") {
				if err := patternExpressions(parameter, parameters); err != nil {
					return err
				}
			}
			bodyScope := cloneNames(parameters)
			if err := collectFunctionVars(node.child("body"), bodyScope); err != nil {
				return err
			}
			return visit(node.child("body"), bodyScope, syntaxNode{}, "", "")
		}
		if kind == "VariableDeclaration" {
			for _, declaration := range node.list("declarations") {
				if err := patternExpressions(declaration.child("id"), outer); err != nil {
					return err
				}
				if initializer := declaration.child("init"); initializer.valid() {
					if err := visit(initializer, outer, syntaxNode{}, "", ""); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if kind == "ClassDeclaration" || kind == "ClassExpression" {
			classScope := cloneNames(outer)
			if id := node.child("id"); id.valid() {
				if err := addPattern(classScope, id); err != nil {
					return err
				}
			}
			if super := node.child("superClass"); super.valid() {
				if err := visit(super, classScope, syntaxNode{}, "", ""); err != nil {
					return err
				}
			}
			return visit(node.child("body"), classScope, syntaxNode{}, "", "")
		}
		if kind == "BlockStatement" || kind == "StaticBlock" {
			blockScope, err := blockNames(node.list("body"), outer)
			if err != nil {
				return err
			}
			if kind == "StaticBlock" {
				for _, item := range node.list("body") {
					if err := collectFunctionVars(item, blockScope); err != nil {
						return err
					}
				}
			}
			for _, statement := range node.list("body") {
				if err := visit(statement, blockScope, syntaxNode{}, "", ""); err != nil {
					return err
				}
			}
			return nil
		}
		if kind == "CatchClause" {
			catchScope := cloneNames(outer)
			if parameter := node.child("param"); parameter.valid() {
				if err := addPattern(catchScope, parameter); err != nil {
					return err
				}
				if err := patternExpressions(parameter, catchScope); err != nil {
					return err
				}
			}
			return visit(node.child("body"), catchScope, syntaxNode{}, "", "")
		}
		shadowed := outer
		if strings.HasPrefix(kind, "For") {
			declaration := node.child("init")
			if !declaration.valid() {
				declaration = node.child("left")
			}
			if declaration.valid() && declaration.kind() == "VariableDeclaration" && declaration.string("kind") != "var" {
				shadowed = cloneNames(outer)
				for _, item := range declaration.list("declarations") {
					if err := addPattern(shadowed, item.child("id")); err != nil {
						return err
					}
				}
			}
		}
		if kind == "SwitchStatement" {
			if err := visit(node.child("discriminant"), outer, syntaxNode{}, "", ""); err != nil {
				return err
			}
			cases := node.list("cases")
			statements := []syntaxNode{}
			for _, item := range cases {
				statements = append(statements, item.list("consequent")...)
			}
			switchScope, err := blockNames(statements, outer)
			if err != nil {
				return err
			}
			for _, item := range cases {
				if err := visit(item, switchScope, syntaxNode{}, "", ""); err != nil {
					return err
				}
			}
			return nil
		}
		if kind == "Property" {
			if node.boolean("computed") {
				if err := visit(node.child("key"), shadowed, syntaxNode{}, "", ""); err != nil {
					return err
				}
			}
			value := node.child("value")
			if node.boolean("shorthand") {
				name := node.child("key").string("name")
				if value.kind() == "AssignmentPattern" {
					if err := visit(value.child("left"), shadowed, value, "left", name); err != nil {
						return err
					}
					return visit(value.child("right"), shadowed, value, "right", "")
				}
				return visit(value, shadowed, node, "value", name)
			}
			return visit(value, shadowed, node, "value", "")
		}
		for _, property := range node.properties() {
			key := property.name
			if kind == "MemberExpression" && key == "property" && !node.boolean("computed") || (kind == "MethodDefinition" || kind == "PropertyDefinition") && key == "key" && !node.boolean("computed") || (kind == "LabeledStatement" || kind == "BreakStatement" || kind == "ContinueStatement") && key == "label" {
				continue
			}
			child := node.view.node(property.value)
			if child.valid() {
				if err := visit(child, shadowed, node, key, ""); err != nil {
					return err
				}
			} else if property.value != nil && property.value.Kind == "array" {
				for _, element := range node.view.nodes(property.value) {
					if err := visit(element, shadowed, node, key, ""); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := visit(ast, namesSet{}, syntaxNode{}, "", ""); err != nil {
		return nil, err
	}
	return edits, nil
}
