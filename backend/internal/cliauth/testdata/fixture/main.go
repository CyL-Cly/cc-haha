package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
)

func privatePath(home, path string) {
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		panic("auth fixture requires existing private HOME")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	if relative, err := filepath.Rel(home, absolute); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
		absolute = filepath.Join(canonical, relative)
	}
	relative, err := filepath.Rel(canonical, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		panic("auth fixture path must remain inside disposable HOME")
	}
	current := canonical
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			panic("auth fixture cannot follow a resource symlink")
		}
	}
}

func main() {
	var input struct {
		Home        string `json:"homeDir"`
		NativeStdio bool   `json:"nativeStdio"`
		Steps       []struct {
			Args        []string        `json:"args"`
			FileEnv     json.RawMessage `json:"fileEnv"`
			File        json.RawMessage `json:"file"`
			Marker      *bool           `json:"marker"`
			Secure      json.RawMessage `json:"secure"`
			SecureError *bool           `json:"secureError"`
			Clear       bool            `json:"clear"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}
	if input.Home == "" || input.Home != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		panic("auth status fixture requires disposable HOME/config")
	}
	homeInfo, err := os.Lstat(input.Home)
	canonicalHome, homeError := filepath.EvalSymlinks(input.Home)
	allocationRoot, rootError := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if err != nil || !homeInfo.IsDir() || homeInfo.Mode()&os.ModeSymlink != 0 || homeError != nil || rootError != nil || !strings.HasPrefix(canonicalHome, allocationRoot+string(filepath.Separator)) {
		panic("auth status fixture requires a private temporary HOME")
	}
	privatePath(input.Home, input.Home)
	privatePath(input.Home, filepath.Join(input.Home, ".claude"))
	tokenFile, tokenFileTwo := filepath.Join(input.Home, "tokens.json"), filepath.Join(input.Home, "tokens-two.json")
	marker := filepath.Join(input.Home, ".claude", "cc-haha", "openai-oauth-file-backed")
	fileEnv, secure, secureError := "", json.RawMessage(nil), false
	calls := []string{}
	store := cliauth.NewOpenAIStorage(cliauth.StorageOptions{
		Home: input.Home, Environment: func(name string) string {
			if name == "OPENAI_CODEX_OAUTH_FILE" {
				return fileEnv
			}
			return os.Getenv(name)
		},
		ReadFile: func(path string) ([]byte, error) { privatePath(input.Home, path); return os.ReadFile(path) },
		Exists:   func(path string) bool { privatePath(input.Home, path); _, err := os.Stat(path); return err == nil },
		ReadSecure: func() (json.RawMessage, error) {
			if secureError {
				return nil, errors.New("fixture secure reader unavailable")
			}
			return secure, nil
		},
		Observe: func(op, path string) {
			if op == "file" {
				key := "one"
				if path == tokenFileTwo {
					key = "two"
				}
				calls = append(calls, "file:"+key)
			} else {
				calls = append(calls, op)
			}
		},
	})
	results := []any{}
	writeNative := func(stdout, stderr string, code int) {
		if len(input.Steps) != 1 {
			panic("native stdio requires one real process-exiting handler")
		}
		evidence, err := json.Marshal(map[string]any{"calls": calls, "blocked": []string{}})
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(input.Home, "native-evidence.json"), evidence, 0600); err != nil {
			panic(err)
		}
		if _, err := os.Stdout.WriteString(stdout); err != nil {
			panic(err)
		}
		if _, err := os.Stderr.WriteString(stderr); err != nil {
			panic(err)
		}
		os.Exit(code)
	}
	for _, step := range input.Steps {
		if step.FileEnv != nil {
			if string(step.FileEnv) == "null" {
				fileEnv = ""
			} else {
				_ = json.Unmarshal(step.FileEnv, &fileEnv)
				fileEnv = strings.ReplaceAll(strings.ReplaceAll(fileEnv, "$TOKEN_FILE_TWO", tokenFileTwo), "$TOKEN_FILE", tokenFile)
			}
		}
		if step.Secure != nil {
			if string(step.Secure) == "null" {
				secure = nil
			} else {
				var data string
				_ = json.Unmarshal(step.Secure, &data)
				secure = json.RawMessage(data)
			}
		}
		if step.SecureError != nil {
			secureError = *step.SecureError
		}
		chosen := strings.TrimSpace(strings.Trim(fileEnv, "\ufeff"))
		if chosen == "" {
			chosen = tokenFile
		}
		privatePath(input.Home, chosen)
		if step.File != nil {
			if string(step.File) == "null" {
				_ = os.Remove(chosen)
			} else {
				var data []byte
				if step.File[0] == '"' {
					var text string
					_ = json.Unmarshal(step.File, &text)
					data = []byte(text)
				} else {
					if err := json.Unmarshal(step.File, &data); err != nil {
						panic(err)
					}
				}
				if err := os.WriteFile(chosen, data, 0600); err != nil {
					panic(err)
				}
			}
		}
		if step.Marker != nil {
			if *step.Marker {
				if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
					panic(err)
				}
				if err := os.WriteFile(marker, []byte("1\n"), 0600); err != nil {
					panic(err)
				}
			} else {
				_ = os.Remove(marker)
			}
		}
		if step.Clear {
			store.Clear()
		}
		calls = []string{}
		args := step.Args
		if args == nil {
			args = []string{"auth", "status", "--openai"}
		}
		parsed := cliargs.Parse(args, cliargs.Config{})
		if parsed.ExitCode != 86 {
			if input.NativeStdio {
				writeNative(parsed.Stdout, parsed.Stderr, parsed.ExitCode)
			}
			results = append(results, map[string]any{"stdout": parsed.Stdout, "stderr": parsed.Stderr, "exitCode": parsed.ExitCode, "calls": []string{}})
			continue
		}
		if parsed.Parsed.Path != "claude auth status" || parsed.Parsed.Options["openai"] != true {
			panic("unsupported business handler")
		}
		result := cliauth.OpenAIStatus(store, parsed.Parsed.Options["text"] == true)
		if input.NativeStdio {
			if result.Diagnostic != nil {
				panic("native diagnostic scope remains pending")
			}
			writeNative(result.Stdout, result.Stderr, result.ExitCode)
		}
		row := map[string]any{"stdout": result.Stdout, "stderr": result.Stderr, "exitCode": result.ExitCode, "calls": calls}
		if result.Diagnostic != nil {
			row["pendingDiagnostic"] = result.Diagnostic
			row["exitCode"] = nil
		}
		results = append(results, row)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"results": results}); err != nil {
		panic(err)
	}
}
