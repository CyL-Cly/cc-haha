// This quality-gate helper uses the pinned Go parser and cover instrumenter.
// It does not import or execute any of the inspected application's code.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type input struct {
	RootDir    string   `json:"rootDir"`
	Files      []string `json:"files"`
	OutputFile string   `json:"outputFile,omitempty"`
}

type executableForm struct {
	Kind string `json:"kind"`
	Line int    `json:"line"`
}

type declarations struct {
	Imports   int `json:"imports"`
	Types     int `json:"types"`
	Constants int `json:"constants"`
	Variables int `json:"variables"`
}

type evidence struct {
	File             string           `json:"file"`
	SHA256           string           `json:"sha256"`
	DeclarationOnly  bool             `json:"declarationOnly"`
	CompilerCounters *int             `json:"compilerCounters"`
	Declarations     declarations     `json:"declarations"`
	Executable       []executableForm `json:"executable"`
}

// Static data syntax contains no evaluated calls, closures, indexing, receives
// or variable reads. Compile-time constant declarations are separately accepted
// only after the lane has successfully built the actual package.
func staticData(expr ast.Expr) bool {
	return staticDataAs(expr, nil)
}

func staticDataAs(expr ast.Expr, expected ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return value.Name == "true" || value.Name == "false" || value.Name == "nil"
	case *ast.ParenExpr:
		return staticData(value.X)
	case *ast.UnaryExpr:
		if value.Op == token.AND {
			_, composite := value.X.(*ast.CompositeLit)
			return composite && staticData(value.X)
		}
		return (value.Op == token.ADD || value.Op == token.SUB || value.Op == token.XOR || value.Op == token.NOT) && staticData(value.X)
	case *ast.BinaryExpr:
		return staticData(value.X) && staticData(value.Y)
	case *ast.CompositeLit:
		typeExpr := value.Type
		if typeExpr == nil {
			typeExpr = expected
		}
		if named, ok := typeExpr.(*ast.Ident); ok && named.Obj != nil {
			if declaration, ok := named.Obj.Decl.(*ast.TypeSpec); ok {
				typeExpr = declaration.Type
			}
		}
		_, isStruct := typeExpr.(*ast.StructType)
		var elementType ast.Expr
		switch declared := typeExpr.(type) {
		case *ast.MapType:
			elementType = declared.Value
		case *ast.ArrayType:
			elementType = declared.Elt
		}
		for _, element := range value.Elts {
			if pair, ok := element.(*ast.KeyValueExpr); ok {
				// Struct field keys are names, not reads of variables.
				_, fieldKey := pair.Key.(*ast.Ident)
				if !(isStruct && fieldKey) && !staticData(pair.Key) || !staticDataAs(pair.Value, elementType) {
					return false
				}
			} else if !staticDataAs(element, elementType) {
				return false
			}
		}
		return true
	}
	return false
}

func inspect(file string, source []byte) (evidence, error) {
	out := evidence{File: file, Executable: []executableForm{}}
	hash := sha256.Sum256(source)
	out.SHA256 = hex.EncodeToString(hash[:])
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, file, source, parser.AllErrors)
	if err != nil {
		return out, err
	}
	for _, declaration := range parsed.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			kind := "function-body"
			if node.Body == nil {
				kind = "external-function-declaration"
			}
			out.Executable = append(out.Executable, executableForm{kind, positions.Position(node.Pos()).Line})
		case *ast.GenDecl:
			switch node.Tok {
			case token.IMPORT:
				out.Declarations.Imports += len(node.Specs)
			case token.TYPE:
				out.Declarations.Types += len(node.Specs)
			case token.CONST:
				for _, spec := range node.Specs {
					out.Declarations.Constants += len(spec.(*ast.ValueSpec).Names)
				}
			case token.VAR:
				for _, spec := range node.Specs {
					values := spec.(*ast.ValueSpec)
					out.Declarations.Variables += len(values.Names)
					for _, value := range values.Values {
						if staticData(value) {
							continue
						}
						found := false
						ast.Inspect(value, func(child ast.Node) bool {
							kind := ""
							switch child.(type) {
							case *ast.CallExpr:
								kind = "initializer-call"
							case *ast.FuncLit:
								kind = "initializer-function-literal"
							}
							if kind != "" {
								found = true
								out.Executable = append(out.Executable, executableForm{kind, positions.Position(child.Pos()).Line})
							}
							return true
						})
						if !found {
							out.Executable = append(out.Executable, executableForm{"non-static-initializer", positions.Position(value.Pos()).Line})
						}
					}
				}
			default:
				return out, fmt.Errorf("unsupported Go declaration %s", node.Tok)
			}
		default:
			return out, fmt.Errorf("unsupported Go AST declaration %T", declaration)
		}
	}
	out.DeclarationOnly = len(out.Executable) == 0
	return out, nil
}

func compilerCounters(path, directory string) (int, error) {
	generated := filepath.Join(directory, "instrumented.go")
	command := exec.Command("go", "tool", "cover", "-mode=atomic", "-var=GoDeclarationCoverage", "-o", generated, path)
	if output, err := command.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("Go cover instrumenter failed: %w: %s", err, output)
	}
	file, err := parser.ParseFile(token.NewFileSet(), generated, nil, parser.AllErrors)
	if err != nil {
		return 0, err
	}
	for _, decl := range file.Decls {
		general, ok := decl.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			value := spec.(*ast.ValueSpec)
			if len(value.Names) != 1 || value.Names[0].Name != "GoDeclarationCoverage" || len(value.Values) != 1 {
				continue
			}
			literal, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			structure, ok := literal.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structure.Fields.List {
				if len(field.Names) != 1 || field.Names[0].Name != "NumStmt" {
					continue
				}
				array, ok := field.Type.(*ast.ArrayType)
				if !ok {
					continue
				}
				length, ok := array.Len.(*ast.BasicLit)
				if !ok || length.Kind != token.INT {
					continue
				}
				return strconv.Atoi(length.Value)
			}
		}
	}
	return 0, fmt.Errorf("Go cover did not produce a recognized counter declaration for %s", path)
}

func run() error {
	var request input
	if len(os.Args) != 2 {
		return fmt.Errorf("expected an explicit declaration input file")
	}
	inputFile, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer inputFile.Close()
	if err := json.NewDecoder(inputFile).Decode(&request); err != nil {
		return err
	}
	if !filepath.IsAbs(request.RootDir) {
		return fmt.Errorf("Go declaration root must be absolute")
	}
	backend := filepath.Join(request.RootDir, "backend")
	directory, err := os.MkdirTemp("", "go-declaration-cover-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	rows := make([]evidence, 0, len(request.Files))
	seen := map[string]bool{}
	for _, name := range request.Files {
		path := filepath.Join(request.RootDir, filepath.FromSlash(name))
		relative, err := filepath.Rel(backend, path)
		if err != nil || filepath.IsAbs(name) || !strings.HasPrefix(name, "backend/") || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || !strings.HasSuffix(name, ".go") || seen[name] {
			return fmt.Errorf("invalid or repeated Go declaration file: %s", name)
		}
		seen[name] = true
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("Go declaration source is not a regular file: %s", name)
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		row, err := inspect(name, source)
		if err != nil {
			return err
		}
		// Inspect executable initializers too. Go's cover instrumenter does not
		// assign package-level initializer calls statement counters; zero here
		// proves that mapping limitation, never that these calls are declarations
		// or that their execution was tested.
		count, err := compilerCounters(path, directory)
		if err != nil {
			return err
		}
		row.CompilerCounters = &count
		if row.DeclarationOnly && count != 0 {
			return fmt.Errorf("declaration AST disagrees with Go cover counters: %s (%d)", name, count)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(source) {
			return fmt.Errorf("Go source changed during declaration inspection: %s", name)
		}
		rows = append(rows, row)
	}
	if request.OutputFile != "" {
		// The caller supplies an exclusive artifact beside its disposable input.
		// A completed process without either receipt remains a hard failure.
		if !filepath.IsAbs(request.OutputFile) || filepath.Dir(filepath.Clean(request.OutputFile)) != filepath.Dir(filepath.Clean(os.Args[1])) || filepath.Base(request.OutputFile) == filepath.Base(os.Args[1]) {
			return fmt.Errorf("Go declaration output must be a separate artifact beside its input")
		}
		output, err := os.OpenFile(request.OutputFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(output).Encode(rows); err != nil {
			output.Close()
			return err
		}
		if err := output.Close(); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(rows)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
