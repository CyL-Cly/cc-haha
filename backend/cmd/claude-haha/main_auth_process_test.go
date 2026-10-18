package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPublicNonModelCLICommandsActualPrivateProcess(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, work, tmp := filepath.Join(root, "home"), filepath.Join(root, "workspace"), filepath.Join(root, "tmp")
	config := filepath.Join(home, ".claude")
	for _, path := range []string{home, work, tmp, config} {
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	var log bytes.Buffer
	build.Stdout = &log
	build.Stderr = &log
	capturePublicGoCoverage(t, build)
	if err = build.Run(); err != nil {
		t.Fatalf("actual CLI build: %v %s", err, log.String())
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1"}
	if rg, err := exec.LookPath("rg"); err == nil {
		env = append(env, "CC_HAHA_RIPGREP_PATH="+rg)
	}
	run := func(extra []string, args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = work
		cmd.Env = append(append([]string{}, env...), extra...)
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		code := 0
		capturePublicGoCoverage(t, cmd)
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		return code, out.String(), stderr.String()
	}
	code, out, stderr := run(nil, "auth", "status")
	var status map[string]any
	if json.Unmarshal([]byte(out), &status) != nil || code != 1 || status["loggedIn"] != false || stderr != "" {
		t.Fatal("anonymous public status", code, out, stderr)
	}
	if runtime.GOOS != "windows" {
		for _, slot := range []string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"} {
			for _, remote := range []bool{false, true} {
				read, write, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := write.WriteString(" \ufefffake-inherited-pipe-credential\u00a0\n"); err != nil {
					t.Fatal(err)
				}
				_ = write.Close()
				cmd := exec.CommandContext(ctx, binary, "auth", "status", "--text")
				cmd.Dir = work
				cmd.Env = append(append([]string{}, env...), slot+"=3tail")
				if remote {
					cmd.Env = append(cmd.Env, "CLAUDE_CODE_REMOTE=1")
				}
				cmd.ExtraFiles = []*os.File{read}
				var out, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &stderr
				capturePublicGoCoverage(t, cmd)
				err = cmd.Run()
				_ = read.Close()
				want := "Auth token: CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR\n"
				filename := ".oauth_token"
				if slot == "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR" {
					want = "API key: ANTHROPIC_API_KEY\n"
					filename = ".api_key"
				}
				if err != nil || out.String() != want || stderr.Len() != 0 {
					t.Fatalf("actual inherited pipe %s remote=%v: %v out=%q stderr=%s", slot, remote, err, out.String(), stderr.String())
				}
				if remote {
					path := filepath.Join(root, "remote", filename)
					data, err := os.ReadFile(path)
					if err != nil || string(data) != "fake-inherited-pipe-credential" {
						t.Fatal("actual CCR persist", err)
					}
					info, err := os.Stat(path)
					if err != nil || info.Mode().Perm() != 0600 {
						t.Fatal("actual CCR credential mode", err)
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	token := filepath.Join(config, "selected-openai.json")
	if err = os.WriteFile(token, []byte(`{"accessToken":"fake-access","refreshToken":"fake-refresh","expiresAt":17,"email":"private@example.invalid","accountId":"private-account"}`), 0600); err != nil {
		t.Fatal(err)
	}
	extra := []string{"OPENAI_CODEX_OAUTH_FILE=" + token}
	code, out, stderr = run(extra, "auth", "status", "--openai", "--text")
	if code != 0 || stderr != "" || !strings.Contains(out, "Account ID: private-account") || strings.Contains(out, "fake-access") {
		t.Fatal("public OpenAI status", code, out, stderr)
	}
	code, out, stderr = run(extra, "auth", "logout", "--openai")
	if code != 0 || stderr != "" || out != "Successfully logged out from your OpenAI account.\n" {
		t.Fatal("public OpenAI logout", code, out, stderr)
	}
	if _, err = os.Stat(token); !os.IsNotExist(err) {
		t.Fatal("public logout did not remove selected file", err)
	}
	code, out, stderr = run(extra, "auth", "status", "--openai")
	if code != 1 || stderr != "" || json.Unmarshal([]byte(out), &status) != nil || status["loggedIn"] != false {
		t.Fatal("public post-logout status", code, out, stderr)
	}
	credential := filepath.Join(config, ".credentials.json")
	if err = os.WriteFile(credential, []byte(`{"claudeAiOauth":{"accessToken":"fake-claude"},"openaiCodexOauth":{"refreshToken":"fake-openai"},"futureSecure":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(config, ".claude.json")
	if err = os.WriteFile(globalPath, []byte(`{"primaryApiKey":"fake-key","oauthAccount":{"emailAddress":"private@example.invalid"},"hasCompletedOnboarding":true,"futureRoot":"preserve"}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr = run(nil, "auth", "logout")
	if code != 0 || stderr != "" || out != "Successfully logged out from your Anthropic account.\n" {
		t.Fatal("public general logout", code, out, stderr)
	}
	if _, err = os.Stat(credential); !os.IsNotExist(err) {
		t.Fatal("public logout retained credentials", err)
	}
	data, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	var global map[string]any
	if json.Unmarshal(data, &global) != nil || global["primaryApiKey"] != nil || global["oauthAccount"] != nil || global["futureRoot"] != "preserve" || global["hasCompletedOnboarding"] != true {
		t.Fatal("public global cleanup damaged unrelated fields", string(data))
	}
	if err = os.MkdirAll(filepath.Join(config, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "agents", "reader.md"), []byte("---\nname: PrivateReader\ndescription: private filesystem observer\nmodel: custom-private-model\n---\nRead private test files."), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr = run(nil, "agents")
	if code != 0 || stderr != "" || !strings.Contains(out, "PrivateReader") || !strings.Contains(out, "custom-private-model") {
		t.Fatal("ordinary public agents listing", code, out, stderr)
	}
	entries, err := os.ReadDir(filepath.Join(config, "projects"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("non-model commands created an ASK session")
	}
}
