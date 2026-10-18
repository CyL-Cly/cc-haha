package securestorage

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

const ownedCredentialsSecurityPython = `#!/usr/bin/python3
import os, sys, json, shlex
args=sys.argv[1:]
state_path=os.environ['OWNED_SECURITY_STATE']; trace_path=os.environ['OWNED_SECURITY_TRACE']
with open(trace_path,'a') as f: f.write(json.dumps({'args':args,'cwd':os.getcwd(),'binary':os.path.basename(os.path.dirname(__file__))},ensure_ascii=False)+'\n')
with open(state_path) as f: state=json.load(f)
def save():
 with open(state_path,'w') as f: json.dump(state,f,ensure_ascii=False,separators=(',',':'))
parts=shlex.split(sys.stdin.read()) if args==['-i'] else args
if len(parts)==8 and parts[:3]==['add-generic-password','-U','-a'] and parts[4]=='-s' and parts[6]=='-X':
 state[parts[3]+'\0'+parts[5]]=bytes.fromhex(parts[7]).decode('utf-8');save();sys.exit(0)
if len(args)==6 and args[:2]==['find-generic-password','-a'] and args[3:5]==['-w','-s']:
 key=args[2]+'\0'+args[5]
 if key not in state: sys.exit(44)
 print(state[key]);sys.exit(int(os.environ['OWNED_FIND_EXIT']))
if len(args)==5 and args[:2]==['delete-generic-password','-a'] and args[3]=='-s':
 key=args[2]+'\0'+args[4]
 if key not in state: sys.exit(44)
 del state[key];save();sys.exit(0)
sys.exit(44)
`

func TestDefaultCredentialsPhysicalProcessAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source Bun required")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	work := filepath.Join(home, "workspace")
	config := filepath.Join(home, ".claude")
	bins := []string{filepath.Join(home, "bin-a"), filepath.Join(home, "bin-b")}
	for _, path := range append([]string{home, work, config}, bins...) {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, bin := range bins {
		if err := os.WriteFile(filepath.Join(bin, "security"), []byte(ownedCredentialsSecurityPython), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "security"), []byte(ownedCredentialsSecurityPython), 0700); err != nil {
		t.Fatal(err)
	}
	statePath, tracePath := filepath.Join(home, "state.json"), filepath.Join(home, "trace.jsonl")
	profiles := []map[string]any{}
	for _, user := range []any{"owned-plain-user", "owned spaced user", "owned-glob-*", "$OWNED_ACCOUNT", "", nil} {
		profiles = append(profiles, map[string]any{"name": len(profiles), "user": user, "findExit": 0})
	}
	profiles = append(profiles, map[string]any{"name": len(profiles), "user": "owned-nonzero-user", "findExit": 44})
	for _, pathKind := range []string{"relative", "leading-empty"} {
		profiles = append(profiles, map[string]any{"name": len(profiles), "user": "owned-path-user", "findExit": 0, "pathKind": pathKind})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-security-physical-storage.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=" + bins[0], "OWNED_SECURITY_STATE=" + statePath, "OWNED_SECURITY_TRACE=" + tracePath, "PYTHONDONTWRITEBYTECODE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_PHYSICAL_STORAGE_SOURCE=1", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "NODE_ENV=production", "DISABLE_TELEMETRY=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": profiles})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("whole physical Source %v %.6500s", err, output)
	}
	type processReceipt struct {
		Args        []string
		Cwd, Binary string
	}
	var original struct {
		Rows []struct {
			Name             int
			User             *string
			FindExit         int
			Service, Account string
			PathKind         string
			Seeds, Final     map[string]string
			Values           []any
			Trace            []processReceipt
		}
		Caps struct{ Blocked, SecureReads []json.RawMessage }
	}
	if err := json.Unmarshal(output, &original); err != nil || len(original.Rows) != 9 || len(original.Caps.Blocked) != 0 || len(original.Caps.SecureReads) != 0 {
		t.Fatalf("whole physical Source boundary %v %.6500s", err, output)
	}
	for _, row := range original.Rows {
		t.Run(string(rune('A'+row.Name)), func(t *testing.T) {
			state, _ := json.Marshal(row.Seeds)
			if err := os.WriteFile(statePath, state, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tracePath, nil, 0600); err != nil {
				t.Fatal(err)
			}
			paths := bins
			executable := filepath.Join(bins[0], "security")
			if row.PathKind == "relative" {
				paths = []string{"../bin-a", "../bin-b"}
			} else if row.PathKind == "leading-empty" {
				paths = []string{":" + bins[0], ":" + bins[1]}
			}
			environment := map[string]string{"HOME": home, "PATH": paths[0], "OWNED_SECURITY_STATE": statePath, "OWNED_SECURITY_TRACE": tracePath, "OWNED_ACCOUNT": "owned-expanded-account", "OWNED_FIND_EXIT": strconv.Itoa(row.FindExit), "PYTHONDONTWRITEBYTECODE": "1"}
			if row.User != nil {
				environment["USER"] = *row.User
			}
			// Inspect the actual command before any spawn: the old hardcoded
			// /usr/bin/security path must never reach the user's real Keychain.
			transport := newSecurityCommand(ctx, Command{Args: []string{"-i"}}, work, environment)
			if transport.Path != executable {
				t.Fatalf("actual default transport bypasses owned current PATH: %q", transport.Path)
			}
			now := time.UnixMilli(1700000000000)
			store, err := New(Options{Home: home, ConfigDir: config, Platform: "darwin", Environment: map[string]string{}, Now: func() time.Time { return now }, NativeKeychainServiceName: func() string { return row.Service }, NativeKeychainUser: func() (string, bool) { value, present := environment["USER"]; return value, present }, NativeSecurityContext: func() (string, map[string]string) { return work, environment }})
			if err != nil {
				t.Fatal(err)
			}
			values := []any{}
			read := func(async bool) {
				var value json.RawMessage
				var err error
				if async {
					value, err = store.ReadSourceJSONAsync(ctx)
				} else {
					value, err = store.ReadSourceJSON(ctx)
				}
				var decoded any
				if err != nil || json.Unmarshal(value, &decoded) != nil {
					t.Fatal("actual store read", string(value), err)
				}
				values = append(values, decoded)
			}
			read(false)
			environment["PATH"] = paths[1]
			store.Clear()
			read(true)
			store.Clear()
			result := store.UpdateSourceJSON(ctx, json.RawMessage(`{"value":"updated","future":{"keep":99}}`))
			updated := map[string]any{"success": result.Success}
			if result.Warning != "" {
				updated["warning"] = result.Warning
			}
			values = append(values, updated)
			read(false)
			values = append(values, store.Delete(ctx))
			store.Clear()
			read(false)
			values = append(values, store.Delete(ctx))
			if !reflect.DeepEqual(values, row.Values) {
				t.Errorf("actual physical store values %v Source %v", values, row.Values)
			}
			content, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatal(err)
			}
			actual := []processReceipt{}
			for _, line := range bytes.Split(bytes.TrimSpace(content), []byte("\n")) {
				var receipt processReceipt
				if err := json.Unmarshal(line, &receipt); err != nil {
					t.Fatal(err)
				}
				actual = append(actual, receipt)
			}
			if !reflect.DeepEqual(actual, row.Trace) {
				t.Errorf("actual physical argv/cwd/executable order %+v Source %+v", actual, row.Trace)
			}
			var final map[string]string
			content, err = os.ReadFile(statePath)
			if err != nil || json.Unmarshal(content, &final) != nil || !reflect.DeepEqual(final, row.Final) {
				t.Error("actual private final state", string(content), row.Final, err)
			}
		})
	}
}
