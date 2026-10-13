// Static inventory only. This program parses Go files without building or
// executing product code. An observed dispatcher/binding is never acceptance.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Location struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function"`
}
type Guard struct {
	Location
	Expression string   `json:"expression"`
	Conditions []string `json:"conditions"`
}
type Function struct {
	Location
	Receiver  string     `json:"receiver"`
	Calls     []string   `json:"calls"`
	Guards    []Guard    `json:"guards"`
	Selectors []Selector `json:"selectors"`
}
type Selector struct {
	Location
	Expression string   `json:"expression"`
	Values     []string `json:"values"`
	Calls      []string `json:"calls"`
}
type Binding struct {
	Location
	Type   string            `json:"type"`
	Fields map[string]string `json:"fields"`
}
type Result struct {
	Functions  []Function        `json:"functions"`
	Bindings   []Binding         `json:"bindings"`
	Files      []string          `json:"files"`
	FileHashes map[string]string `json:"fileHashes"`
}

var fset = token.NewFileSet()

func code(n ast.Node) string {
	var b bytes.Buffer
	if n != nil {
		_ = printer.Fprint(&b, fset, n)
	}
	return b.String()
}
func callName(n *ast.CallExpr) string { return code(n.Fun) }
func calls(n ast.Node) []string {
	seen := map[string]bool{}
	ast.Inspect(n, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			seen[callName(c)] = true
		}
		return true
	})
	out := []string{}
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	result := Result{Functions: []Function{}, Bindings: []Binding{}, Files: []string{}, FileHashes: map[string]string{}}
	err := filepath.WalkDir(filepath.Join(root, "backend"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		input, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, input, 0)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		result.Files = append(result.Files, rel)
		result.FileHashes[rel] = fmt.Sprintf("%x", sha256.Sum256(input))
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			loc := Location{rel, fset.Position(fn.Pos()).Line, fn.Name.Name}
			entry := Function{Location: loc, Calls: calls(fn.Body), Guards: []Guard{}, Selectors: []Selector{}}
			if fn.Recv != nil {
				entry.Receiver = code(fn.Recv.List[0].Type)
			}
			stack := []ast.Node{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				at := loc
				at.Line = fset.Position(n.Pos()).Line
				switch n := n.(type) {
				case *ast.CallExpr:
					name := callName(n)
					if name == "unsupported" || name == "mainCap" || strings.HasSuffix(name, ".capability") || name == "capability" {
						conditions := []string{}
						for _, p := range stack {
							switch p := p.(type) {
							case *ast.IfStmt:
								conditions = append(conditions, code(p.Cond))
							case *ast.CaseClause:
								for _, v := range p.List {
									conditions = append(conditions, "case "+code(v))
								}
							}
						}
						entry.Guards = append(entry.Guards, Guard{at, code(n), conditions})
					}
				case *ast.SwitchStmt:
					for _, s := range n.Body.List {
						c := s.(*ast.CaseClause)
						values := []string{}
						for _, e := range c.List {
							if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
								v, e := strconv.Unquote(lit.Value)
								if e == nil {
									values = append(values, v)
								}
							}
						}
						if len(values) > 0 {
							entry.Selectors = append(entry.Selectors, Selector{at, code(n.Tag), values, calls(c)})
						}
					}
				case *ast.CompositeLit:
					typ := code(n.Type)
					if strings.HasSuffix(typ, "Options") {
						fields := map[string]string{}
						for _, el := range n.Elts {
							if kv, ok := el.(*ast.KeyValueExpr); ok {
								fields[code(kv.Key)] = code(kv.Value)
							}
						}
						result.Bindings = append(result.Bindings, Binding{at, typ, fields})
					}
				}
				return true
			})
			result.Functions = append(result.Functions, entry)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
