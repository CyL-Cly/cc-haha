package application

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopServerConfigDirectoryDefaultsAndOverride(t *testing.T) {
	for _, name := range []string{"absent", "absolute_override"} {
		t.Run(name, func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			home := o.Environment["HOME"]
			config := filepath.Join(home, ".claude")
			otherConfig := filepath.Join(home, "unused-config")
			if name == "absolute_override" {
				config, otherConfig = otherConfig, config
				o.Environment["CLAUDE_CONFIG_DIR"] = config
			} else {
				delete(o.Environment, "CLAUDE_CONFIG_DIR")
			}
			o.Environment["PATH"] = "/usr/bin:/bin"
			o.Environment["CC_HAHA_DISABLE_TERMINAL_SHELL_ENV"] = "1"
			originalEnvironment := make(map[string]string, len(o.Environment))
			for key, value := range o.Environment {
				originalEnvironment[key] = value
			}
			write := func(path, text string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			projectID := projectpath.Sanitize(o.WorkDir)
			memoryDir := filepath.Join(config, "projects", projectID, "memory")
			for _, root := range []string{config, otherConfig} {
				write(filepath.Join(root, "settings.json"), `{"enabledPlugins":{},"disableAgentSkillsDirectory":true}`)
			}
			write(filepath.Join(config, "skills", "selected-config-probe", "SKILL.md"), "---\ndescription: Selected disposable configuration root\n---\nSelected instructions.\n")
			write(filepath.Join(otherConfig, "skills", "unselected-config-probe", "SKILL.md"), "---\ndescription: Wrong disposable configuration root\n---\nUnselected instructions.\n")
			write(filepath.Join(memoryDir, "MEMORY.md"), "Selected disposable memory root.\n")
			write(filepath.Join(otherConfig, "projects", projectID, "memory", "MEMORY.md"), "Unselected disposable memory root.\n")
			write(filepath.Join(o.WorkDir, ".git", "HEAD"), "ref: refs/heads/private-config-test\n")
			var nativeEnvironments []map[string]string
			service, err := NewDesktopServer(ServerOptions{
				Environment: o.Environment, HomeDir: home, ConfigDir: config,
				WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments,
				RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
					return nil, os.ErrNotExist
				},
				NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
					native, err := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{
						Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot,
						ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil },
					})
					if err == nil {
						nativeEnvironments = append(nativeEnvironments, native.Environment)
					}
					return native, err
				},
			})
			if err != nil {
				t.Fatalf("NewDesktopServer rejected %s configuration root: %v", name, err)
			}
			t.Cleanup(func() { _ = service.Close(context.Background()) })
			host := httptest.NewServer(service)
			t.Cleanup(host.Close)
			get := func(path string, result any) {
				t.Helper()
				response, err := host.Client().Get(host.URL + path)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil || response.StatusCode != http.StatusOK {
					t.Fatalf("GET %s: status=%d body=%s err=%v", path, response.StatusCode, body, err)
				}
				if err := json.Unmarshal(body, result); err != nil {
					t.Fatalf("GET %s returned invalid JSON: %v", path, err)
				}
			}
			var skills struct {
				Skills []struct{ Name, Source string }
			}
			get("/api/skills", &skills)
			if len(skills.Skills) != 1 || skills.Skills[0].Name != "selected-config-probe" || skills.Skills[0].Source != "user" {
				t.Fatalf("Skills used the wrong configuration root: %+v", skills)
			}
			var memory struct {
				Projects []struct {
					ID, MemoryDir     string
					FileCount         int
					Exists, IsCurrent bool
				}
			}
			get("/api/memory/projects?cwd="+url.QueryEscape(o.WorkDir), &memory)
			if len(memory.Projects) != 1 || memory.Projects[0].ID != projectID || memory.Projects[0].MemoryDir != memoryDir || memory.Projects[0].FileCount != 1 || !memory.Projects[0].Exists || !memory.Projects[0].IsCurrent {
				t.Fatalf("Memory used the wrong configuration root: %+v", memory)
			}
			var targets struct {
				Platform  string
				Targets   []json.RawMessage
				TTLMillis int64 `json:"ttlMs"`
			}
			get("/api/open-targets", &targets)
			platform := runtime.GOOS
			if platform == "windows" {
				platform = "win32"
			}
			if targets.Platform != platform || targets.Targets == nil || targets.TTLMillis != 30000 {
				t.Fatalf("Open Targets was not bound to the native configuration consumer: %+v", targets)
			}
			if !reflect.DeepEqual(o.Environment, originalEnvironment) || len(nativeEnvironments) == 0 {
				t.Fatalf("Server rewrote the raw launch environment: %+v", o.Environment)
			}
			for _, env := range nativeEnvironments {
				if !reflect.DeepEqual(env, originalEnvironment) {
					t.Fatalf("Native consumer rewrote the raw launch environment: %+v", env)
				}
			}
		})
	}
}

func TestRunServerConfigDirectoryPresenceResolvesOnePhysicalRoot(t *testing.T) {
	for _, name := range []string{"absent", "empty", "absolute_override", "relative_override"} {
		t.Run(name, func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			home := o.Environment["HOME"]
			config := filepath.Join(home, ".claude")
			switch name {
			case "absent":
				delete(o.Environment, "CLAUDE_CONFIG_DIR")
			case "empty":
				o.Environment["CLAUDE_CONFIG_DIR"] = ""
				config = o.WorkDir
			case "absolute_override":
				config = filepath.Join(home, "config-override")
				o.Environment["CLAUDE_CONFIG_DIR"] = config
			case "relative_override":
				o.Environment["CLAUDE_CONFIG_DIR"] = "relative-config"
				config = filepath.Join(o.WorkDir, "relative-config")
			}
			write := func(path, text string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), mode); err != nil {
					t.Fatal(err)
				}
			}
			// RunServer owns its credential primitive. Keep its real command path
			// private even when an explicit empty/relative config cannot opt into
			// the deliberately narrower absolute-path SmokeSecurity profile.
			bin := filepath.Join(home, "private-bin")
			write(filepath.Join(bin, "security"), "#!/bin/sh\nexit 44\n", 0700)
			o.Environment["PATH"] = bin + string(os.PathListSeparator) + "/usr/bin:/bin"
			o.Environment["CC_HAHA_DISABLE_TERMINAL_SHELL_ENV"] = "1"
			originalEnvironment := make(map[string]string, len(o.Environment))
			for key, value := range o.Environment {
				originalEnvironment[key] = value
			}
			for _, root := range []string{filepath.Join(home, ".claude"), o.WorkDir, filepath.Join(home, "config-override"), filepath.Join(o.WorkDir, "relative-config")} {
				write(filepath.Join(root, "settings.json"), `{"language":"wrong-config-root","enabledPlugins":{},"disableAgentSkillsDirectory":true}`, 0600)
			}
			write(filepath.Join(config, "settings.json"), `{"language":"selected-config-root","enabledPlugins":{},"disableAgentSkillsDirectory":true}`, 0600)
			write(filepath.Join(config, "skills", "entrypoint-config-probe", "SKILL.md"), "---\ndescription: Selected actual server configuration root\n---\nPrivate entrypoint instructions.\n", 0600)
			write(filepath.Join(o.WorkDir, ".git", "HEAD"), "ref: refs/heads/private-entrypoint-config\n", 0600)
			reservation, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := reservation.Addr().(*net.TCPAddr).Port
			if err := reservation.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			var nativeEnvironments []map[string]string
			var nativeMu sync.Mutex
			go func() {
				result <- RunServer(ctx, []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port)}, o.Environment, o.WorkDir, o.Version, io.Discard, func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
					snapshot := make(map[string]string, len(env))
					for key, value := range env {
						snapshot[key] = value
					}
					nativeMu.Lock()
					nativeEnvironments = append(nativeEnvironments, snapshot)
					nativeMu.Unlock()
					return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
				})
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-result:
					case <-time.After(5 * time.Second):
						t.Error("private configuration RunServer cleanup did not join")
					}
				}
			}()
			base := "http://127.0.0.1:" + strconv.Itoa(port)
			client := &http.Client{Timeout: time.Second}
			defer client.CloseIdleConnections()
			deadline := time.Now().Add(10 * time.Second)
			for {
				response, err := client.Get(base + "/health")
				if err == nil {
					response.Body.Close()
					if response.StatusCode != http.StatusOK {
						t.Fatalf("private configuration RunServer health: %d", response.StatusCode)
					}
					break
				}
				select {
				case err := <-result:
					joined = true
					t.Fatalf("RunServer rejected %s configuration before HTTP: %v", name, err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("private configuration RunServer never became ready")
				}
				time.Sleep(20 * time.Millisecond)
			}
			get := func(path string, value any) {
				t.Helper()
				response, err := client.Get(base + path)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil || response.StatusCode != http.StatusOK {
					t.Fatalf("GET %s: status=%d body=%s err=%v", path, response.StatusCode, body, err)
				}
				if err := json.Unmarshal(body, value); err != nil {
					t.Fatalf("GET %s returned invalid JSON: %v", path, err)
				}
			}
			var settings struct{ Language string }
			get("/api/settings/user", &settings)
			if settings.Language != "selected-config-root" {
				t.Errorf("RunServer %s Settings disagrees with Source configuration root %s: %+v", name, config, settings)
			}
			var skills struct {
				Skills []struct{ Name, Source string }
			}
			get("/api/skills", &skills)
			if len(skills.Skills) != 1 || skills.Skills[0].Name != "entrypoint-config-probe" || skills.Skills[0].Source != "user" {
				t.Errorf("RunServer %s Skills disagrees with Source configuration root %s: %+v", name, config, skills)
			}
			cancel()
			select {
			case err := <-result:
				joined = true
				if err != nil {
					t.Fatal("private configuration RunServer shutdown", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("private configuration RunServer did not join shutdown")
			}
			nativeMu.Lock()
			defer nativeMu.Unlock()
			if !reflect.DeepEqual(o.Environment, originalEnvironment) || len(nativeEnvironments) == 0 {
				t.Fatalf("RunServer changed its raw environment or skipped native consumers: %+v", o.Environment)
			}
			for _, env := range nativeEnvironments {
				if !reflect.DeepEqual(env, originalEnvironment) {
					t.Fatalf("RunServer changed a native consumer's raw environment: %+v", env)
				}
			}
		})
	}
}
