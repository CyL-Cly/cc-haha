package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/powershellpolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourcePowerShellConfiguredASTDeadlineIsNotCappedByExecutionPrimitive(t *testing.T) {
	interpreter := physicalPowerShell(t)
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	s, owner, home, _ := fixture(t)
	executable := filepath.Join(home, "pwsh")
	// The executable-discovery seam delays native startup. It then executes only
	// the exact original data-only AST parser argv on the installed interpreter.
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n/bin/sleep 5.25\nexec '"+strings.ReplaceAll(interpreter, "'", "'\"'\"'")+"' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-powershell-policy.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "executable": executable, "parserEnvironmentRows": []map[string]any{{"command": "Get-Process", "timeout": "12000", "results": nil}}}))
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatal("actual delayed Source AST producer", err, string(out))
	}
	var source struct {
		ParserEnvironmentRows []struct {
			Result   powershellpolicy.Parsed
			Timeouts []float64
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.ParserEnvironmentRows) != 1 || !source.ParserEnvironmentRows[0].Result.Valid || !reflect.DeepEqual(source.ParserEnvironmentRows[0].Timeouts, []float64{12000}) {
		t.Fatal("original configurable AST producer receipt", err, string(out))
	}
	environment := map[string]string{"HOME": home, "USERPROFILE": home, "DOTNET_CLI_HOME": home, "PATH": "/usr/bin:/bin", "CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS": "12000"}
	s.powerShellParser = powershellpolicy.NewParser(powershellpolicy.ParserOptions{Executable: executable, CWD: home, Environment: environment, Run: s.runPowerShellASTProcess})
	s.options.ProcessEnvironment = func(*cooperative.Scope) (map[string]string, error) { return environment, nil }
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		parser, _, err := s.powerShellPolicy(scope)
		if err != nil {
			return err
		}
		future, err := native(s, scope, func() (powershellpolicy.Parsed, error) { return parser.Parse(context.Background(), "Get-Process"), nil })
		if err != nil {
			return err
		}
		parsed, err := future.Await(scope)
		if err == nil && !reflect.DeepEqual(parsed, source.ParserEnvironmentRows[0].Result) {
			return fmt.Errorf("native parser truncated Source configured deadline: %+v", parsed)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPowerShellCanceledPermissionAwaitKeepsNativeASTJobOwnedUntilDelivery(t *testing.T) {
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("physical PowerShell host required")
	}
	s, owner, home, permissions := fixture(t)
	permissions.Set("mode", quote("default"))
	refs := &ownerasync.NativeReferences{}
	binding, err := refs.Bind(owner)
	if err != nil {
		t.Fatal(err)
	}
	s.options.NativeBinding = binding
	started, finished, release := make(chan struct{}), make(chan powershellpolicy.ProcessResult, 1), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	s.powerShellParser = powershellpolicy.NewParser(powershellpolicy.ParserOptions{Executable: executable, CWD: home, Environment: s.options.Environment, Run: func(ctx context.Context, path string, args []string, environment map[string]string, cwd string) (powershellpolicy.ProcessResult, error) {
		close(started)
		result, err := s.runPowerShellASTProcess(ctx, path, args, environment, cwd)
		finished <- result
		<-release // Only delivery scheduling is controlled; the physical AST process is real.
		return result, err
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			input := protocol.NewJSONObject(field("command", "Get-Process"))
			_, err := s.PermissionDecision(scope, "PowerShell", &input)
			return err
		})
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("native AST job was not admitted")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("permission wait cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled permission waiter retained the owner")
	}
	select {
	case result := <-finished:
		if result.TimedOut || result.ExitCode != 0 || result.Stdout == "" {
			t.Fatal("canceled waiter killed independent Source AST process", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("bounded physical AST process did not complete")
	}
	count, idle := refs.Snapshot()
	if count != 1 {
		t.Fatal("physical work lost its teardown obligation before delivery", count)
	}
	owner.Close()
	close(release)
	released = true
	select {
	case <-idle:
	case <-time.After(10 * time.Second):
		t.Fatal("retired owner could not reclaim actual AST completion")
	}
	if count, _ := refs.Snapshot(); count != 0 {
		t.Fatal("actual native AST completion leaked references", count)
	}
}

func TestActualSourcePowerShellCurrentUserTypeRefreshChangesPermissionWithoutReparsing(t *testing.T) {
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("physical PowerShell host required")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	s, owner, home, permissions := fixture(t)
	permissions.Set("mode", quote("default"))
	rows := []map[string]any{}
	for _, userType := range []string{"", "ant", "private"} {
		rows = append(rows, map[string]any{"input": map[string]string{"command": "gh"}, "context": map[string]string{"mode": "default"}, "userType": userType})
	}
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-powershell-policy.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "executable": executable, "normalizedEvaluations": rows}))
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source current USER_TYPE permissions", err, string(out))
	}
	var source struct {
		NormalizedEvaluations []struct{ Result json.RawMessage }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.NormalizedEvaluations) != len(rows) {
		t.Fatal("Source mutable USER_TYPE corpus", err, string(out))
	}
	environment := map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "CLAUDE_CODE_POWERSHELL_PATH": executable}
	s.options.ProcessEnvironment = func(*cooperative.Scope) (map[string]string, error) { return environment, nil }
	processes := 0
	s.powerShellParser = powershellpolicy.NewParser(powershellpolicy.ParserOptions{Executable: executable, CWD: home, Environment: environment, Run: func(ctx context.Context, path string, args []string, environment map[string]string, cwd string) (powershellpolicy.ProcessResult, error) {
		processes++
		return s.runPowerShellASTProcess(ctx, path, args, environment, cwd)
	}})
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		for i, row := range rows {
			environment["USER_TYPE"] = row["userType"].(string)
			input := protocol.NewJSONObject(field("command", "gh"))
			decision, err := s.PermissionDecision(scope, "PowerShell", &input)
			if err != nil {
				return err
			}
			var got, want any
			_ = json.Unmarshal(raw(powerShellDecisionProjection(decision)), &got)
			_ = json.Unmarshal(source.NormalizedEvaluations[i].Result, &want)
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("Source mutable USER_TYPE %q native=%s Source=%s", environment["USER_TYPE"], raw(powerShellDecisionProjection(decision)), source.NormalizedEvaluations[i].Result)
			}
		}
		return nil
	})
	if err != nil || processes != 1 {
		t.Fatal("current environment permissions must share the original AST cache", err, processes)
	}
}

func TestActualSourcePowerShellEnvironmentChangedWhileNativeParserYieldedOwner(t *testing.T) {
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("physical PowerShell host required")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	s, owner, home, permissions := fixture(t)
	permissions.Set("mode", quote("default"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-powershell-policy.fixture.ts")
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "executable": executable, "parserUserTypeAfterSpawn": "ant", "normalizedEvaluations": []map[string]any{{"input": map[string]string{"command": "gh"}, "context": map[string]string{"mode": "default"}, "userType": ""}}}))
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source yielded environment producer", err, string(out))
	}
	var source struct {
		NormalizedEvaluations []struct{ Result json.RawMessage }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.NormalizedEvaluations) != 1 {
		t.Fatal("Source yielded environment receipt", err, string(out))
	}
	environment := map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "CLAUDE_CODE_POWERSHELL_PATH": executable}
	s.options.ProcessEnvironment = func(*cooperative.Scope) (map[string]string, error) { return environment, nil }
	started, resume := make(chan struct{}), make(chan struct{})
	resumed := false
	defer func() {
		if !resumed {
			close(resume)
		}
	}()
	s.powerShellParser = powershellpolicy.NewParser(powershellpolicy.ParserOptions{Executable: executable, CWD: home, Environment: environment, Run: func(ctx context.Context, path string, args []string, environment map[string]string, cwd string) (powershellpolicy.ProcessResult, error) {
		close(started)
		result, err := s.runPowerShellASTProcess(ctx, path, args, environment, cwd)
		<-resume
		return result, err
	}})
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			input := protocol.NewJSONObject(field("command", "gh"))
			decision, err := s.PermissionDecision(scope, "PowerShell", &input)
			if err != nil {
				return err
			}
			var got, want any
			_ = json.Unmarshal(raw(powerShellDecisionProjection(decision)), &got)
			_ = json.Unmarshal(source.NormalizedEvaluations[0].Result, &want)
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("Source read-only callback lost settings changed while parsing: native=%s Source=%s", raw(powerShellDecisionProjection(decision)), source.NormalizedEvaluations[0].Result)
			}
			return nil
		})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("native AST job not admitted", ctx.Err())
	}
	settingsReceipt, err := owner.SubmitIndependent(ctx, cooperative.Input, func(*cooperative.Scope) error { environment["USER_TYPE"] = "ant"; return nil })
	if err != nil {
		t.Fatal("settings could not run while parser yielded the owner", err)
	}
	if err = settingsReceipt.Wait(); err != nil {
		t.Fatal("admitted settings callback did not finish", err)
	}
	close(resume)
	resumed = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual parser settings continuation did not settle", ctx.Err())
	}
}
