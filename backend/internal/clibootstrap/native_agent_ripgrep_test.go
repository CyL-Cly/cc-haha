package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeAgentRipgrepResolverMatchesActualSourceAndRealChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows native executable fixture requires Windows runner")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	for _, scenario := range []string{"explicit", "sibling", "virtual-explicit", "invalid-explicit", "system-only", "system-version-fallback", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativePluginFixture(t)
			_ = os.MkdirAll(f.n.Temp, 0700)
			bundled := filepath.Join(f.home, "package", "rg")
			explicit := filepath.Join(f.home, "explicit", "rg")
			first := filepath.Join(f.home, "bad-system", "rg")
			system := filepath.Join(f.home, "system", "rg")
			file := filepath.Join(f.cwd, "agents", "worker.md")
			_ = os.MkdirAll(filepath.Dir(file), 0700)
			_ = os.WriteFile(file, []byte("worker"), 0600)
			script := func(path, version string) {
				t.Helper()
				_ = os.MkdirAll(filepath.Dir(path), 0700)
				data := "#!/bin/sh\nif [ \"$1\" = '--version' ]; then printf '%s\\n' '" + version + "'; else printf '%s\\n' '" + file + "'; fi\n"
				if err := os.WriteFile(path, []byte(data), 0700); err != nil {
					t.Fatal(err)
				}
			}
			script(bundled, "ripgrep bundled")
			script(explicit, "ripgrep explicit")
			script(first, "incorrect-binary")
			script(system, "ripgrep fixture")
			f.n.Environment["PATH"] = filepath.Dir(first) + string(os.PathListSeparator) + filepath.Dir(system)
			f.n.Environment["CC_HAHA_RIPGREP_PATH"] = explicit
			switch scenario {
			case "sibling":
				delete(f.n.Environment, "CC_HAHA_RIPGREP_PATH")
			case "virtual-explicit":
				f.n.Environment["CC_HAHA_RIPGREP_PATH"] = filepath.Join(f.home, "$bunfs", "rg")
			case "invalid-explicit":
				f.n.Environment["CC_HAHA_RIPGREP_PATH"] = filepath.Join(f.home, "missing")
			case "system-only", "system-version-fallback":
				f.n.Environment["USE_BUILTIN_RIPGREP"] = " false "
				if scenario == "system-only" {
					f.n.Environment["PATH"] = filepath.Dir(system)
				}
			case "unavailable":
				f.n.Environment["USE_BUILTIN_RIPGREP"] = "0"
				f.n.Environment["PATH"] = filepath.Dir(first)
			}
			executable := filepath.Join(filepath.Dir(bundled), "go-server")
			got, err := nativeAgentRipgrepConfig(f.n, executable, "")
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-ripgrep.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = f.home
			cmd.Env = nativeAgentProcessEnvironment(f.n)
			cmd.Env = append(cmd.Env, "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(f.home), "CC_HAHA_NATIVE_AGENT_RIPGREP_FIXTURE=1", "NODE_ENV=production", "USERPROFILE="+f.home, "TMPDIR="+f.n.Temp, "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
			data, _ := json.Marshal(map[string]string{"homeDir": f.home, "executable": executable})
			cmd.Stdin = bytes.NewReader(data)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, e := cmd.Output()
			if e != nil {
				t.Fatalf("Source ripgrep %v %.2000s", e, stderr.String())
			}
			var source struct {
				Config struct {
					RGPath string   `json:"rgPath"`
					RGArgs []string `json:"rgArgs"`
				}
				Capabilities struct{ Blocked []string }
			}
			if json.Unmarshal(out, &source) != nil || len(source.Capabilities.Blocked) > 0 {
				t.Fatalf("Source invalid/escaped %.2000s", out)
			}
			if got.Command != source.Config.RGPath || got.Command != "" && !reflect.DeepEqual(got.Args, source.Config.RGArgs) || (err != nil) != (source.Config.RGPath == "") {
				t.Fatalf("Go %+v %v Source %+v", got, err, source.Config)
			}
			if err == nil {
				command := exec.CommandContext(ctx, got.Command, append(got.Args, "--files", "--hidden", "--follow", "--no-ignore", "--glob", "*.md", filepath.Dir(file))...)
				command.Env = nativeAgentProcessEnvironment(f.n)
				output, e := command.Output()
				if e != nil || strings.TrimSpace(string(output)) != file {
					t.Fatalf("actual child %s %v", output, e)
				}
			}
		})
	}
}
func TestNativeAgentRipgrepVendorAndConfiguredDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows native child fixture requires Windows runner")
	}
	f := newNativePluginFixture(t)
	_ = os.MkdirAll(f.n.Temp, 0700)
	root := filepath.Join(f.home, "vendor", "ripgrep")
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	binary := filepath.Join(root, arch+"-"+runtime.GOOS, "rg")
	_ = os.MkdirAll(filepath.Dir(binary), 0700)
	file := filepath.Join(f.cwd, "agents", "actual.md")
	_ = os.MkdirAll(filepath.Dir(file), 0700)
	_ = os.WriteFile(file, []byte("actual"), 0600)
	// This actual executable is deliberately outside PATH and only accepts
	// packaged --no-config followed by Source discovery arguments.
	script := "#!/bin/sh\nif [ \"$1\" = '--no-config' ] && [ \"$2\" = '--files' ] && [ \"$7\" = '*.md' ]; then printf '%s\\n' '" + file + "'; else exit 7; fi\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	f.n.Environment["PATH"] = ""
	config, err := nativeAgentRipgrepConfig(f.n, filepath.Join(f.home, "absent-package", "server"), root)
	if err != nil || config.Command != binary || len(config.Args) != 0 {
		t.Fatalf("standalone vendor %+v %v", config, err)
	}
	f.n.Environment["CC_HAHA_RIPGREP_PATH"] = binary
	files, err := nativeAgentMarkdown(f.n, filepath.Dir(file))
	if err != nil || !reflect.DeepEqual(files, []string{file}) {
		t.Fatalf("configured actual discovery %v %v", files, err)
	}
}
