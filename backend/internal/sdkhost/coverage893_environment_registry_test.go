package sdkhost

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCoverage893PublicEnvironmentProjectionPreservesCallerMap(t *testing.T) {
	original := map[string]string{"ANTHROPIC_API_KEY": "offline-key", "ANTHROPIC_BASE_URL": "http://127.0.0.1:9", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli", "API_TIMEOUT_MS": "19", "NODE_EXTRA_CA_CERTS": "/private/fixture.pem", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_SIMPLE": "1", "HOME": "/private/home", "USER_TYPE": "external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_ENABLE_STREAM_WATCHDOG": "0", "GOCOVERDIR": "/private/coverage", "TEST_TELEMETRY_DIR": "/private/off"}
	before := map[string]string{}
	for k, v := range original {
		before[k] = v
	}
	projected, e := publicPrintRequestEnvironment(original)
	if e != nil {
		t.Fatal(e)
	}
	want := map[string]string{"ANTHROPIC_API_KEY": "offline-key", "ANTHROPIC_BASE_URL": "http://127.0.0.1:9", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli", "API_TIMEOUT_MS": "19", "NODE_EXTRA_CA_CERTS": "/private/fixture.pem", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_SIMPLE": "1"}
	if !reflect.DeepEqual(projected, want) || !reflect.DeepEqual(original, before) {
		t.Fatal(projected, original)
	}
	projected["ANTHROPIC_API_KEY"] = "changed"
	if original["ANTHROPIC_API_KEY"] != "offline-key" {
		t.Fatal("projection aliases caller map")
	}
	for _, tc := range []struct{ key, value, boundary string }{{"CLAUDE_CODE_ENTRYPOINT", "cli", "entrypoint"}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "0", "telemetry"}, {"ANTHROPIC_AUTH_TOKEN", "fake", "original environment"}} {
		t.Run(tc.key, func(t *testing.T) {
			m := map[string]string{tc.key: tc.value}
			_, e := publicPrintRequestEnvironment(m)
			var cap *CapabilityError
			if !errors.As(e, &cap) || !strings.Contains(cap.Boundary, tc.boundary) || m[tc.key] != tc.value {
				t.Fatal(m, e)
			}
		})
	}
}

func TestCoverage893RegistryPhysicalDiscoveryAndSnapshotIsolation(t *testing.T) {
	for _, kind := range []string{"skills", "workflows", "agents", ".agents"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			cwd := filepath.Join(home, "workspace")
			p := filepath.Join(cwd, ".claude", kind)
			if kind == ".agents" {
				p = filepath.Join(cwd, ".agents", "skills")
			}
			if e := os.MkdirAll(p, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(p, "private-entry"), []byte("not executed"), 0600); e != nil {
				t.Fatal(e)
			}
			_, e := NewPrintRegistry(PrintRegistryOptions{Home: home, CWD: cwd, Stat: os.Stat, ReadDir: os.ReadDir, EntryPoint: "sdk-cli", UserType: "external"})
			var cap *CapabilityError
			if !errors.As(e, &cap) {
				t.Fatal("active private discovery admitted", e)
			}
		})
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace")
	if e := os.MkdirAll(filepath.Join(cwd, ".claude", "skills"), 0700); e != nil {
		t.Fatal(e)
	}
	reason := errors.New("private directory enumeration failed")
	r, e := NewPrintRegistry(PrintRegistryOptions{Home: home, CWD: cwd, Stat: os.Stat, ReadDir: func(p string) ([]fs.DirEntry, error) {
		if strings.HasSuffix(p, "/.claude/skills") {
			return nil, reason
		}
		return os.ReadDir(p)
	}, EntryPoint: "sdk-cli", UserType: "external", BuiltinAgentsDisabled: true})
	if e != nil || len(r.Catalog().Agents) != 0 || len(r.DiscoveryErrors()) != 1 || r.DiscoveryErrors()[0] != reason.Error() {
		t.Fatal(r, e)
	}
	c := r.Catalog()
	c.Commands[0].Name = "changed"
	d := r.DiscoveryErrors()
	d[0] = "changed"
	if r.Catalog().Commands[0].Name == "changed" || r.DiscoveryErrors()[0] != reason.Error() {
		t.Fatal("caller mutated retained discovery/catalog")
	}
	p := filepath.Join(cwd, ".agents", "skills")
	if e := os.MkdirAll(p, 0700); e != nil {
		t.Fatal(e)
	}
	_, e = NewPrintRegistry(PrintRegistryOptions{Home: home, CWD: cwd, Stat: os.Stat, ReadDir: func(path string) ([]fs.DirEntry, error) {
		if path == p {
			return nil, reason
		}
		return os.ReadDir(path)
	}, EntryPoint: "sdk-cli", UserType: "external"})
	if e != reason {
		t.Fatal(".agents enumeration error identity lost", e)
	}
}
