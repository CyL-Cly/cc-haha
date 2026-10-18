package securestorage

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const ownedLegacySecurityPython = `#!/usr/bin/python3
import os, sys, json, shlex
args=sys.argv[1:]
state_path=os.environ['OWNED_SECURITY_STATE']; trace_path=os.environ['OWNED_SECURITY_TRACE']
with open(trace_path,'a') as f: f.write(json.dumps({'args':args,'cwd':os.getcwd()},ensure_ascii=False)+'\n')
with open(state_path) as f: state=json.load(f)
def save():
 with open(state_path,'w') as f: json.dump(state,f,ensure_ascii=False,separators=(',',':'))
if args==['-i']:
 parts=shlex.split(sys.stdin.read())
 if len(parts)!=8 or parts[:3]!=['add-generic-password','-U','-a'] or parts[4]!='-s' or parts[6]!='-X': sys.exit(44)
 state[parts[3]+'\0'+parts[5]]=bytes.fromhex(parts[7]).decode('utf-8');save();sys.exit(0)
if len(args)==6 and args[:2]==['find-generic-password','-a'] and args[3:5]==['-w','-s']:
 key=args[2]+'\0'+args[5]
 if key not in state: sys.exit(44)
 print(state[key]);sys.exit(0)
if len(args)==5 and args[:2]==['delete-generic-password','-a'] and args[3]=='-s':
 key=args[2]+'\0'+args[4]
 if key not in state: sys.exit(44)
 del state[key];save();sys.exit(0)
sys.exit(44)
`

func TestLegacyCurrentIdentityShellAndStdinAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source Bun required")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	work := filepath.Join(home, "workspace")
	bin := filepath.Join(home, "bin")
	for _, path := range []string{home, work, bin} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"owned-glob-a", "owned-glob-b"} {
		if err := os.WriteFile(filepath.Join(work, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(bin, "security")
	if err := os.WriteFile(executable, []byte(ownedLegacySecurityPython), 0700); err != nil {
		t.Fatal(err)
	}
	state, trace := filepath.Join(home, "state.json"), filepath.Join(home, "trace.jsonl")
	profiles := []map[string]any{}
	for _, directory := range []any{filepath.Join(home, "next"), filepath.Join(home, "e\u0301"), "owned-relative", "", nil} {
		for _, user := range []any{"owned-next-account", "owned-用户-é", "", nil, "owned spaced account", "owned-glob-*"} {
			profiles = append(profiles, map[string]any{"name": len(profiles), "directory": directory, "user": user})
		}
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-legacy-keychain-current.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	environment := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + bin + ":/usr/bin:/bin", "OWNED_SECURITY_STATE=" + state, "OWNED_SECURITY_TRACE=" + trace, "PYTHONDONTWRITEBYTECODE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_LEGACY_KEYCHAIN_CURRENT_SOURCE=1", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir, cmd.Env = home, environment
	input, _ := json.Marshal(map[string]any{"home": home, "rows": profiles})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("whole original legacy Source %v %.9000s", err, output)
	}
	type primitiveTrace struct {
		Args []string
		Cwd  string
	}
	var source struct {
		Rows []struct {
			Name                                   int
			Initial, InitialService, TargetService string
			TargetAccount, Key                     string
			Target, User, Fallback                 *string
			Seeds, Final                           map[string]string
			Values                                 []any
			Trace                                  []primitiveTrace
		}
		Caps struct{ Blocked, SecureReads []json.RawMessage }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != 30 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("actual original boundary %v %.9000s", err, output)
	}
	for _, row := range source.Rows {
		t.Run(string(rune('A'+row.Name)), func(t *testing.T) {
			initial, _ := json.Marshal(row.Seeds)
			if err := os.WriteFile(state, initial, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(trace, nil, 0600); err != nil {
				t.Fatal(err)
			}
			user, present := "owned-old-account", true
			// Credentials suffix placement is deliberately supplied by the real
			// helper, not an expected hash recomputed in this physical test.
			service := strings.Replace(row.InitialService, "Claude Code", "Claude Code-credentials", 1)
			store, err := New(Options{Home: home, ConfigDir: row.Initial, ConfigDirExplicit: true, Platform: "darwin", Environment: map[string]string{}, NativeKeychainServiceName: func() string { return service }, NativeKeychainUser: func() (string, bool) { return user, present }, RunSecurity: func(callContext context.Context, command Command) ([]byte, error) {
				var child *exec.Cmd
				if command.ShellScript != "" {
					child = exec.CommandContext(callContext, "/bin/sh", "-c", command.ShellScript)
				} else {
					child = exec.CommandContext(callContext, executable, command.Args...)
				}
				child.Dir = work
				child.Env = append([]string{}, environment...)
				if present {
					child.Env = append(child.Env, "USER="+user)
				}
				if command.Input != "" {
					child.Stdin = strings.NewReader(command.Input)
				}
				return child.Output()
			}})
			if err != nil {
				t.Fatal(err)
			}
			values := []any{}
			read := func() {
				key, err := store.ReadLegacyManagedAPIKey(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if key == "" {
					if row.Fallback == nil {
						values = append(values, nil)
						return
					}
					key = *row.Fallback
				}
				values = append(values, key)
			}
			read()
			user, present = "", row.User != nil
			if row.User != nil {
				user = *row.User
			}
			service = strings.Replace(row.TargetService, "Claude Code", "Claude Code-credentials", 1)
			read()
			if ok, err := store.SaveLegacyManagedAPIKey(ctx, row.Key); err != nil || !ok {
				t.Fatal("same original rejecting-false writer", ok, err)
			}
			read()
			if err := store.RemoveLegacyManagedAPIKey(ctx); err != nil {
				values = append(values, err.Error())
			} else {
				values = append(values, nil)
			}
			read()
			if !reflect.DeepEqual(values, row.Values) {
				t.Errorf("actual read/save/remove results %v Source %v", values, row.Values)
			}
			var final map[string]string
			raw, err := os.ReadFile(state)
			if err != nil || json.Unmarshal(raw, &final) != nil || !reflect.DeepEqual(final, row.Final) {
				t.Error("actual private keychain entries", string(raw), row.Final, err)
			}
			raw, err = os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			var actual []primitiveTrace
			for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
				var item primitiveTrace
				if err := json.Unmarshal(line, &item); err != nil {
					t.Fatal(err)
				}
				actual = append(actual, item)
			}
			if !reflect.DeepEqual(actual, row.Trace) {
				t.Errorf("physical security argv/cwd/order %+v Source %+v", actual, row.Trace)
			}
		})
	}
}

func TestLegacyDefaultSecurityTransportUsesCurrentNativeContext(t *testing.T) {
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	initialWork := filepath.Join(home, "old-work")
	currentWork := filepath.Join(home, "current-work")
	currentDirectory := currentWork
	bin := filepath.Join(home, "bin")
	for _, path := range []string{home, initialWork, currentWork, bin} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte(ownedLegacySecurityPython), 0700); err != nil {
		t.Fatal(err)
	}
	statePath, tracePath := filepath.Join(home, "state.json"), filepath.Join(home, "trace.jsonl")
	state, _ := json.Marshal(map[string]string{"owned-current-user\x00Claude Code-current": "owned-current-key"})
	if err := os.WriteFile(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	currentEnvironment := map[string]string{
		"HOME": home, "USER": "owned-current-user", "PATH": bin + ":/usr/bin:/bin",
		"OWNED_SECURITY_STATE": statePath, "OWNED_SECURITY_TRACE": tracePath,
		"PYTHONDONTWRITEBYTECODE": "1",
	}
	store, err := New(Options{
		Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "darwin",
		Environment:               map[string]string{"USER": "owned-old-user", "PATH": "/owned-absent-path"},
		NativeKeychainServiceName: func() string { return "Claude Code-credentials-current" },
		NativeKeychainUser: func() (string, bool) {
			value, present := currentEnvironment["USER"]
			return value, present
		},
		NativeSecurityContext: func() (string, map[string]string) { return currentDirectory, currentEnvironment },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key, err := store.ReadLegacyManagedAPIKey(ctx)
	if err != nil || key != "owned-current-key" {
		t.Fatalf("default physical reader retained launch context: %q %v", key, err)
	}
	if err := store.RemoveLegacyManagedAPIKey(ctx); err != nil {
		t.Fatal("default physical remover", err)
	}
	key, err = store.ReadLegacyManagedAPIKey(ctx)
	if err != nil || key != "" {
		t.Fatalf("original rejecting-false read after deletion: %q %v", key, err)
	}
	content, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(content), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("actual private process receipts=%d", len(lines))
	}
	physicalWork, err := filepath.EvalSymlinks(currentWork)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range lines {
		var observed struct {
			Args []string
			Cwd  string
		}
		if err := json.Unmarshal(line, &observed); err != nil {
			t.Fatal(err)
		}
		wanted := []string{"find-generic-password", "-a", "owned-current-user", "-w", "-s", "Claude Code-current"}
		if i == 1 {
			wanted = []string{"delete-generic-password", "-a", "owned-current-user", "-s", "Claude Code-current"}
		}
		if observed.Cwd != physicalWork || !reflect.DeepEqual(observed.Args, wanted) {
			t.Fatalf("physical process %d: %+v", i, observed)
		}
	}
	// Reject an invalid native context before spawning, without touching the
	// host's actual security executable or creating another process receipt.
	currentDirectory = "owned-relative"
	if _, err := store.ReadLegacyManagedAPIKey(ctx); err == nil {
		t.Fatal("relative current context admitted")
	}
	after, err := os.ReadFile(tracePath)
	if err != nil || !bytes.Equal(after, content) {
		t.Fatal("invalid context spawned a native process", err)
	}
}
