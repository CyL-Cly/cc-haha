package curepl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/javascriptparse"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const firstUUID = "11111111-1111-4111-8111-111111111111"
const secondUUID = "22222222-2222-4222-8222-222222222222"

func compilerSource(t *testing.T, input any) []json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-repl-compiler.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_CU_COMPILER_FIXTURE=1"}
	raw, _ := json.Marshal(input)
	command.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Source compiler %v %s", err, stderr.String())
	}
	var out []json.RawMessage
	if err = json.Unmarshal(output, &out); err != nil {
		t.Fatalf("Source compiler output %s %v", output, err)
	}
	return out
}
func compilerDecoded(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
func TestCompilerExactUTF16AgainstOriginalSource(t *testing.T) {
	type scenario struct {
		Code  json.RawMessage `json:"code"`
		Prior []Binding       `json:"prior"`
		UUIDs []string        `json:"uuids"`
	}
	prior := []Binding{{"count", "const"}, {"previous", "let"}, {"other", "const"}}
	codes := []string{
		"",
		"first()\nsecond()",
		"first() // comment\nsecond()",
		"let value = first()\nsecond(value)",
		"var value = first()\nsecond(value)",
		"function run() { return first()\nsecond() }\nsecond(run())",
		"try { throw first()\nsecond() } catch (value) { second(value) }",
		"if (true) first()\nelse second()\nsecond()",
		"for (let i = 0; i < 2; i++) first()\nsecond()",
		"for (var value of [1, 2]) first()\nsecond(value)",
		"for (var value in {a:1}) { first()\nsecond(value) }",
		"first()\nsecond" + string(rune(96)) + "text" + string(rune(96)),
		"const {x, nested: {y = 2}, ...rest} = source; let [first, ...tail] = values; async function act(){const local=1; return local} class Target {} for(var index of [1,2]){var visited=index;let scoped=3} {let hidden=4;function nested(){}}",
		"const app = await select(); let next=app",
		"count++",
		"({value: count} = source)",
		"function increment(){count++}",
		"count.value++",
		"function local(count){count++}",
		"function local(){count++;var count=0}",
		"{let count=0;count++}",
		"for(let count=0;count<2;count++){}",
		"try{}catch(count){count++}",
		"count+=1; other--",
		"const count = 3",
		"let count = count + 1",
		"const { [source.key]: value = missing, ...rest } = container",
		"let [a=source, , ...tail]=items",
		"const fn=(value=source)=>value + count",
		"const fn = function named({x = count}){ return named(x) + other }",
		"function f(){return arguments[0] + count}",
		"const f = () => arguments[0]",
		"class Target extends Base { method(v=source){return count + this.value} [methodName](){return other} field=count; static { var local=source; local++; other++ } }",
		"const Thing=class Inner extends Base { method(){return Inner} }",
		"switch(value){case count: let hidden=source; use(hidden); break; default: use(hidden);}",
		"for(const {item = source} of data){use(item)}",
		"for(var {item} of data) use(item)",
		"for(var item in data) {use(item)}",
		"for(var i=0;i<count;i++) use(i)",
		"future=1; throw new Error('stop'); var future=2",
		"future++; var future",
		"({a:future}=source);var future",
		"future ||= source; throw new Error('stop'); var future",
		"future &&= source; var future",
		"future ??= source; var future",
		"var x=1,y=source; var x=2; function f(){return x}",
		"throw new Error('stop'); function later(){return count}",
		"obj.method(count); obj[count](other); call?.(count); new Constructor(source)",
		"({ count, other, [key]: value, get x(){return count} })",
		"({count = source} = container)",
		"typeof missing; typeof count; typeof (count); function f(count){return typeof count} {let missing;typeof missing}",
		"label: for(const value of data){if(value) break label;continue label}",
		"async function f({x = source}={}){await x;return ()=>other}",
		"function f(){ {var shadow=count} return shadow }",
		"const emoji='😀'; const 𐐀 = source; use(𐐀, emoji)",
		"const \\u0061 = source; use(a)",
		"\ufeffconst x = source\r\nuse(x)",
		"const literal = /a+/g; literal.test(text)",
		"import fs from 'node:fs'",
		"await import('node:fs')",
		"async function later(){return import('node:fs')}",
		"export const x=1",
		"export default function(){}",
		"import.meta.url",
		"return 42",
		"await action(); const broken=",
		"const __cu_cell_11111111_1111_4111_8111_111111111111_scope=1; use(__cu_cell_11111111_1111_4111_8111_111111111111_scope)",
	}
	rows := []scenario{}
	for _, code := range codes {
		rows = append(rows, scenario{protocol.QuoteJavaScriptUTF16(units(code)), prior, []string{firstUUID, secondUUID}})
	}
	lone := append(units("const text='"), 0xd800)
	lone = append(lone, units("'; use(text)")...)
	rows = append(rows, scenario{protocol.QuoteJavaScriptUTF16(lone), prior, []string{firstUUID, secondUUID}})
	got := []any{}
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		parser, err := javascriptparse.New(scope, owner)
		if err != nil {
			return err
		}
		for _, row := range rows {
			code, err := protocol.DecodeJavaScriptJSONString(row.Code)
			if err != nil {
				return err
			}
			parsed, err := parser.ParseModule(scope, code)
			if err != nil {
				return err
			}
			calls := 0
			if !parsed.Accepted {
				got = append(got, map[string]any{"error": map[string]any{"name": parsed.Failure.Name, "message": string(utf16.Decode(parsed.Failure.Message))}, "uuidCalls": calls})
				continue
			}
			value, err := CompileAST(code, parsed.AST, row.Prior, CompilerOptions{RandomUUID: func() (string, error) {
				if calls >= len(row.UUIDs) {
					return "", fmt.Errorf("Private UUID fixture exhausted")
				}
				id := row.UUIDs[calls]
				calls++
				return id, nil
			}})
			if err != nil {
				got = append(got, map[string]any{"error": map[string]any{"name": "Error", "message": err.Error()}, "uuidCalls": calls})
			} else {
				got = append(got, map[string]any{"value": map[string]any{"bindings": value.Bindings, "warnings": value.Warnings, "source": value.Source}, "uuidCalls": calls})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := compilerSource(t, map[string]any{"cases": rows})
	if len(want) != len(got) {
		t.Fatal("compiler observation count", len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(compilerDecoded(got[i]), compilerDecoded(want[i])) {
			actual, _ := json.Marshal(got[i])
			t.Fatalf("compiler case %d code %s\nGo %s\nSource %s", i, rows[i].Code, actual, want[i])
		}
	}
}
func TestCompilerDefaultPrivateNamesAndNoMutation(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		parser, err := javascriptparse.New(scope, owner)
		if err != nil {
			return err
		}
		code := units("let count=1; count++")
		parsed, err := parser.ParseModule(scope, code)
		if err != nil {
			return err
		}
		before, _ := json.Marshal(parsed.AST)
		prior := []Binding{{"prior", "let"}}
		one, err := CompileAST(code, parsed.AST, prior, CompilerOptions{})
		if err != nil {
			return err
		}
		two, err := CompileAST(code, parsed.AST, prior, CompilerOptions{})
		if err != nil {
			return err
		}
		after, _ := json.Marshal(parsed.AST)
		if string(before) != string(after) || prior[0].Name != "prior" || string(utf16.Decode(code)) != "let count=1; count++" {
			t.Fatal("compiler mutated caller AST/prior/source")
		}
		if reflect.DeepEqual(one.Source, two.Source) || !strings.Contains(string(utf16.Decode(one.Source)), "__cu_cell_") {
			t.Fatal("private prefixes not generated")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompilerBindingReplacementAndPrefixCollisionMatrixAgainstSource(t *testing.T) {
	type scenario struct {
		Code  string    `json:"code"`
		Prior []Binding `json:"prior"`
		UUIDs []string  `json:"uuids"`
	}
	priorSets := [][]Binding{
		{},
		{{"a", "const"}, {"b", "let"}, {"c", "var"}},
		{{"a", "let"}, {"b", "const"}, {"c", "class"}},
		{{"a", "const"}, {"a", "const"}, {"b", "function"}, {"fn", "const"}},
	}
	codes := []string{
		"const a=1; let b=a; var c=b; a++",
		"b++; a++; b--; const a=2",
		"let a=()=>b; class c extends Base{}; function fn(){return a()}",
		"var a; {var b=source;} a ||= b; for(var c of data) c++",
		"({a,b,...c}=source); function local(a){return b++}",
		"for(a of values){b++} for(c in object){a++}",
		"var a; throw new Error('error'); var b; function fn(){}",
		"function fn({a = b},...c){const b=1;return [a,b,c,arguments]}",
		"class Named { static { var a=source; a++; b++; } field=c; method(){return b} }",
		"const fn = async(a=b)=>{await a; return ()=>c}",
		"if(test){let a=b;use(a)} else {const b=a;use(b)}",
		"({a, b, nested: {c}, method(){return a}, [c]:b})",
	}
	rows := []scenario{}
	for _, prior := range priorSets {
		for _, code := range codes {
			rows = append(rows, scenario{code, prior, []string{firstUUID, secondUUID}})
		}
	}
	for _, suffix := range []string{"_scopeFactory", "_scope", "_register", "_mark", "_value"} {
		name := "__cu_cell_11111111_1111_4111_8111_111111111111" + suffix
		for _, code := range []string{"let " + name + "=source; use(" + name + ")", "obj." + name + "; obj.source()", "function nested(" + name + "){return " + name + "}", "({ " + name + ": value })"} {
			rows = append(rows, scenario{code, []Binding{}, []string{firstUUID, secondUUID}})
		}
	}
	rows = append(rows, scenario{"const __cu_cell_11111111_1111_4111_8111_111111111111_scope=1", []Binding{}, []string{firstUUID}})
	got := []any{}
	owner := cooperative.New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		parser, err := javascriptparse.New(scope, owner)
		if err != nil {
			return err
		}
		for _, row := range rows {
			parsed, err := parser.ParseModule(scope, units(row.Code))
			if err != nil {
				return err
			}
			calls := 0
			if !parsed.Accepted {
				got = append(got, map[string]any{"error": map[string]any{"name": parsed.Failure.Name, "message": string(utf16.Decode(parsed.Failure.Message))}, "uuidCalls": calls})
				continue
			}
			value, err := CompileAST(units(row.Code), parsed.AST, row.Prior, CompilerOptions{RandomUUID: func() (string, error) {
				if calls >= len(row.UUIDs) {
					return "", fmt.Errorf("Private UUID fixture exhausted")
				}
				id := row.UUIDs[calls]
				calls++
				return id, nil
			}})
			if err != nil {
				got = append(got, map[string]any{"error": map[string]any{"name": "Error", "message": err.Error()}, "uuidCalls": calls})
			} else {
				got = append(got, map[string]any{"value": map[string]any{"bindings": value.Bindings, "warnings": value.Warnings, "source": value.Source}, "uuidCalls": calls})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := compilerSource(t, map[string]any{"cases": rows})
	for i := range got {
		if !reflect.DeepEqual(compilerDecoded(got[i]), compilerDecoded(want[i])) {
			g, _ := json.Marshal(got[i])
			t.Fatalf("matrix case %d code %s\nGo %s\nSource %s", i, rows[i].Code, g, want[i])
		}
	}
}
