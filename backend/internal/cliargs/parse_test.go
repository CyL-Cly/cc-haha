package cliargs

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestGuardedParseAndCommanderErrors(t *testing.T) {
	cases := []struct {
		args    []string
		code    int
		message string
	}{
		{nil, 86, ""}, {[]string{"hello"}, 86, ""}, {[]string{"mcp", "get", "name"}, 86, ""},
		{[]string{"plugins", "i", "example"}, 86, ""},
		{[]string{"mcp"}, 1, "Usage:"}, {[]string{"mcp", "unknown"}, 1, "unknown command"},
		{[]string{"mcp", "gett"}, 1, "Did you mean get"},
		{[]string{"mcp", "get"}, 1, "missing required argument 'name'"},
		{[]string{"one", "two"}, 1, "Expected 1 argument but got 2"},
		{[]string{"mcp", "get", "one", "two"}, 1, "too many arguments for 'get'"},
		{[]string{"--output-format"}, 1, "argument missing"},
		{[]string{"--output-format", "invalid"}, 1, "Allowed choices are text, json, stream-json."},
		{[]string{"--ouput-format", "json"}, 1, "Did you mean --output-format"},
		{[]string{"--unknown"}, 1, "unknown option"},
		{[]string{"--agent-teams"}, 1, "unknown option '--agent-teams'"},
		{[]string{"--print=false"}, 1, "unknown option"},
		{[]string{"mcp", "get", "name", "--verbose"}, 1, "unknown option"},
		{[]string{"--help"}, 0, ""}, {[]string{"mcp", "help", "get"}, 0, ""},
		{[]string{"mcp", "help", "unknown"}, 1, "Usage:"},
		{[]string{"mcp", "help"}, 0, ""},
		{[]string{"plugin", "unknown"}, 1, "unknown command"},
		{[]string{"--unknown", "--help"}, 0, ""},
		{[]string{"--output-format", "--help"}, 1, "Allowed choices"},
		{[]string{"-v"}, 0, ""}, {[]string{"-V"}, 0, ""}, {[]string{"--version"}, 0, ""}, {[]string{"--version", "--unknown"}, 0, ""},
		{[]string{"-V", "--unknown"}, 1, "unknown option"},
		{[]string{"--", "--unknown"}, 86, ""},
		{[]string{"mcp", "add", "my", "command", "--", "--flag"}, 86, ""},
		{[]string{"--output-format=invalid"}, 1, "Allowed choices"},
		{[]string{"-pc"}, 86, ""}, {[]string{"-vignored"}, 0, ""},
		{[]string{"--unknown", "--", "literal"}, 1, "unknown option"},
	}
	for _, test := range cases {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			result := Parse(test.args, Config{})
			if result.ExitCode != test.code || !strings.Contains(result.Stderr, test.message) {
				t.Fatalf("%v => %+v", test.args, result)
			}
		})
	}
}

func TestOptionValuesAliasesDefaultsAndCobraDispatch(t *testing.T) {
	result := Parse([]string{"--plugin-dir", "one", "--plugin-dir=two", "--allowedTools", "Read", "Write", "--disallowed-tools=Edit", "--no-session-persistence", "--chrome", "--no-chrome", "--from-pr=", "-dp", "--worktree", "-2", "--max-turns", "0x10", "--debug-to-stderr"}, Config{})
	if result.ExitCode != 86 || result.Parsed == nil {
		t.Fatalf("%+v", result)
	}
	values := result.Parsed.Options
	expected := map[string]any{"pluginDir": []any{"one", "two"}, "allowedTools": []any{"Read", "Write"}, "disallowedTools": []any{"Edit"}, "sessionPersistence": false, "chrome": false, "computerUse": true, "fromPr": true, "debug": true, "worktree": "-2", "maxTurns": float64(16), "debugToStderr": false, "enableAuthStatus": false}
	for key, value := range expected {
		actual, _ := json.Marshal(values[key])
		want, _ := json.Marshal(value)
		if string(actual) != string(want) {
			t.Errorf("%s: %s != %s", key, actual, want)
		}
	}
	local := Parse([]string{"--verbose", "mcp", "serve", "--debug"}, Config{})
	if local.ExitCode != 86 || local.Parsed.Globals["verbose"] != true || local.Parsed.Options["debug"] != true {
		t.Fatalf("%+v", local)
	}
	for _, profile := range []string{"normal", "bare", "print", "xaa"} {
		command, err := NewCommand(profile)
		if err != nil {
			t.Fatal(err)
		}
		if command.Name() != "claude" || command.Flags().Lookup("allowedTools") == nil || command.Flags().Lookup("allowed-tools") == nil {
			t.Fatal("Cobra option aliases missing")
		}
		if command.Version != "999.0.0-local (Claude Code)" || command.Flags().Lookup("allowedTools").Value.Type() != "commander" {
			t.Fatal("Cobra registration metadata differs")
		}
		flag := command.Flags().Lookup("version")
		if flag.Value.Type() != "bool" || flag.Value.String() != "false" {
			t.Fatal("Cobra reserved version binding is not a false bool")
		}
		if err := flag.Value.Set("invalid"); err == nil {
			t.Fatal("invalid bool binding accepted")
		}
		if err := flag.Value.Set("true"); err != nil || flag.Value.String() != "true" {
			t.Fatal("valid bool binding rejected")
		}
		if err := command.Flags().Lookup("allowedTools").Value.Set("Read"); err != nil {
			t.Fatal(err)
		}
		if profile == "print" && len(command.Commands()) != 0 {
			t.Fatal("print profile has commands")
		}
	}
	if _, err := NewCommand("unknown"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestPureNumberAndCustomParserSemantics(t *testing.T) {
	for input, want := range map[string]float64{"": 0, "  7 ": 7, "0x10": 16, "0o10": 8, "0b10": 2, "-0": 0, ".5": 0.5, "1e2": 100, "+Infinity": math.Inf(1), "-Infinity": math.Inf(-1)} {
		if got := jsNumber(input); got != want {
			t.Errorf("%q => %v, want %v", input, got, want)
		}
	}
	for _, input := range []string{"invalid", "Inf", "0x+1", "0x1p2", "0xZZ", "1_000"} {
		if !math.IsNaN(jsNumber(input)) {
			t.Errorf("expected NaN: %q", input)
		}
	}
	for _, test := range []struct {
		args  []string
		value any
	}{
		{[]string{"--max-turns", "not-number"}, nil}, {[]string{"--max-turns", "Infinity"}, nil},
		{[]string{"--deep-link-last-fetch", "invalid"}, ""}, {[]string{"--deep-link-last-fetch", "7"}, float64(7)},
		{[]string{"--deep-link-last-fetch", "Infinity"}, ""},
		{[]string{"--effort", "-" + strings.Repeat("9", 400)}, nil},
		{[]string{"--effort", " XHIGH "}, "xhigh"}, {[]string{"--effort", "ULTRACODE"}, "ultracode"}, {[]string{"--effort", "-7"}, float64(-7)},
		{[]string{"--max-budget-usd", "0x10"}, float64(16)}, {[]string{"--task-budget", "2"}, float64(2)},
	} {
		result := Parse(test.args, Config{})
		if test.args[0] == "--effort" && len(test.args[1]) > 400 {
			if result.ExitCode != 1 || result.Diagnostic != nil {
				t.Fatal("effort overflow accepted", result)
			}
			continue
		}
		if result.ExitCode != 86 {
			t.Fatalf("%v => %+v", test.args, result)
		}
		key := map[string]string{"--max-turns": "maxTurns", "--deep-link-last-fetch": "deepLinkLastFetch", "--effort": "effort", "--max-budget-usd": "maxBudgetUsd", "--task-budget": "taskBudget"}[test.args[0]]
		if result.Parsed.Options[key] != test.value {
			t.Errorf("%v => %#v, want %#v", test.args, result.Parsed.Options[key], test.value)
		}
	}
	if result := Parse([]string{"--effort", "invalid"}, Config{}); result.ExitCode != 1 || result.Diagnostic != nil || !strings.Contains(result.Stderr, "It must be one of") {
		t.Fatal(result)
	}
	for _, args := range [][]string{{"--max-budget-usd", "0"}, {"--max-budget-usd", "NaN"}, {"--task-budget", "-1"}, {"--task-budget", "1.5"}, {"--task-budget", "Infinity"}} {
		result := Parse(args, Config{})
		if result.ExitCode != 1 || result.Diagnostic == nil || result.Diagnostic.StdioComplete {
			t.Fatalf("raw stack must remain explicitly pending: %+v", result)
		}
	}
}

func TestXAAMandatoryRegistration(t *testing.T) {
	if result := Parse([]string{"mcp", "xaa", "setup"}, Config{XAA: true}); result.ExitCode != 1 || !strings.Contains(result.Stderr, "required option") {
		t.Fatal(result)
	}
	result := Parse([]string{"mcp", "xaa", "setup", "--issuer", "https://issuer.invalid", "--client-id", "client"}, Config{XAA: true})
	if result.ExitCode != 86 {
		t.Fatalf("%+v", result)
	}
	if result := Parse([]string{"-p", "mcp"}, Config{XAA: true}); result.ExitCode != 86 || result.Parsed.Path != "claude" {
		t.Fatal(result)
	}
}

func TestSpecificationRejectsUnsupportedRegistration(t *testing.T) {
	if _, err := LoadSpecification([]byte("invalid")); err == nil {
		t.Fatal("malformed spec accepted")
	}
	if _, err := LoadSpecification([]byte(`{"schemaVersion":2,"profiles":{}}`)); err == nil {
		t.Fatal("unknown schema accepted")
	}
	for _, mutation := range []func(*CommandSpec){
		func(node *CommandSpec) { node.Name = "" },
		func(node *CommandSpec) { node.Options[0].ParserKind = "unknown" },
		func(node *CommandSpec) { node.Options[0].Attribute = "" },
		func(node *CommandSpec) { node.Options[0].Long = nil; node.Options[0].Short = nil },
		func(node *CommandSpec) { node.Options[0].ParserKind = "choices"; node.Options[0].Choices = nil },
		func(node *CommandSpec) { node.Options = append(node.Options, node.Options[0]) },
		func(node *CommandSpec) { node.Commands = append(node.Commands, nil) },
		func(node *CommandSpec) { value := "ENV"; node.Options[0].Environment = &value },
		func(node *CommandSpec) { node.Options[0].Conflicts = []string{"other"} },
		func(node *CommandSpec) { node.Options[0].Implies = map[string]any{"other": true} },
		func(node *CommandSpec) { node.Options[0].Preset = true },
		func(node *CommandSpec) { node.Arguments[0].ParserKind = "unsupported" },
		func(node *CommandSpec) { node.Commands[0].HelpOption = "--unknown-help" },
	} {
		var spec Spec
		if err := json.Unmarshal(Specification(), &spec); err != nil {
			t.Fatal(err)
		}
		mutation(spec.Profiles["normal"])
		data, _ := json.Marshal(spec)
		if _, err := LoadSpecification(data); err == nil {
			t.Fatal("unsupported spec accepted")
		}
	}
	var spec Spec
	if err := json.Unmarshal(Specification(), &spec); err != nil {
		t.Fatal(err)
	}
	spec.Profiles["normal"] = nil
	data, _ := json.Marshal(spec)
	if _, err := LoadSpecification(data); err == nil {
		t.Fatal("missing named profile accepted")
	}
}
