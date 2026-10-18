package securestorage

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeConfigKeychainIdentityAndUnchangedCacheAgainstWholeSource(t *testing.T) {
	empty, named := "", "owned-launch-user"
	for _, launch := range []struct {
		name string
		user *string
	}{{"absent-launch", nil}, {"empty-launch", &empty}, {"named-launch", &named}} {
		t.Run(launch.name, func(t *testing.T) {
			testNativeConfigKeychainSource(t, launch.user)
		})
	}
}

func testNativeConfigKeychainSource(t *testing.T, launchUser *string) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-keychain-runtime.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_CONFIG_KEYCHAIN_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	if launchUser != nil {
		cmd.Env = append(cmd.Env, "USER="+*launchUser)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual private Keychain Source %v %.8000s", err, output)
	}
	type call struct{ Operation, Account, Service, Mode string }
	var source struct {
		Rows []struct {
			Kind, Mode                    string
			Username                      bool
			TargetUser                    *string
			TargetAccount                 string
			Initial, PhysicalTarget       string
			Target                        *string
			InitialService, TargetService string
			OldPlain, NewPlain            string
			Seeds, Primary                [][2]string
			Update                        json.RawMessage
			Values                        []json.RawMessage
			Calls                         []call
			InitialDisk, TargetDisk       *string
		}
		Caps struct{ Blocked, SecureReads []json.RawMessage }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != 23 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("invalid Source/private boundary %v %.7500s", err, output)
	}
	for _, row := range source.Rows {
		t.Run(row.Kind+"/"+row.Mode, func(t *testing.T) {
			for path, data := range map[string]string{filepath.Join(row.Initial, ".credentials.json"): row.OldPlain, filepath.Join(row.PhysicalTarget, ".credentials.json"): row.NewPlain} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			primary := map[string]string{}
			for _, pair := range row.Seeds {
				primary[pair[0]] = pair[1]
			}
			calls := []call{}
			now := time.UnixMilli(1700000000000)
			directory, mkdirDirectory, service := row.Initial, row.Initial, row.InitialService
			user, userPresent := "owned-keychain-user", true
			entryKey := func(account, service string) string {
				if row.Username {
					return account + "\x00" + service
				}
				return service
			}
			readMode := "sync"
			launchEnvironment := map[string]string{"USER": "owned-keychain-user"}
			if row.Username {
				// Subsequent Source environment assignments do not change its
				// absent/empty/named launch userInfo snapshot.
				if launchUser == nil {
					delete(launchEnvironment, "USER")
				} else {
					launchEnvironment["USER"] = *launchUser
				}
			}
			store, err := New(Options{Home: home, ConfigDir: row.Initial, Platform: "darwin", ConfigDirExplicit: true, Environment: launchEnvironment, Now: func() time.Time { return now }, NativePlaintextLocation: func() (string, string) { return mkdirDirectory, filepath.Join(directory, ".credentials.json") }, NativeKeychainServiceName: func() string { return service }, NativeKeychainUser: func() (string, bool) { return user, userPresent }, RunSecurity: func(_ context.Context, command Command) ([]byte, error) {
				operation, mode := "", readMode
				account, name := "", ""
				switch command.Args[0] {
				case "find-generic-password":
					operation = "read"
					account, name = command.Args[2], command.Args[5]
				case "delete-generic-password":
					operation, mode = "delete", "sync"
					account, name = command.Args[2], command.Args[4]
				case "-i":
					operation, mode = "update", "stdin"
					parts := strings.Split(command.Input, `"`)
					if len(parts) != 7 {
						return nil, errors.New("invalid private keychain writer")
					}
					account, name = parts[1], parts[3]
					calls = append(calls, call{operation, account, name, mode})
					if row.Mode == "seeded-fallback" {
						return nil, errors.New("private keychain writer unavailable")
					}
					payload, err := hex.DecodeString(parts[5])
					if err != nil {
						return nil, err
					}
					primary[entryKey(account, name)] = string(payload)
					return nil, nil
				default:
					return nil, errors.New("unowned keychain primitive")
				}
				calls = append(calls, call{operation, account, name, mode})
				if operation == "delete" {
					if _, exists := primary[entryKey(account, name)]; !exists {
						return nil, errors.New("private entry absent")
					}
					delete(primary, entryKey(account, name))
					return nil, nil
				}
				return []byte(primary[entryKey(account, name)]), nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			values := []json.RawMessage{}
			read := func(async bool) {
				readMode = "sync"
				var raw json.RawMessage
				var err error
				if async {
					readMode = "async"
					raw, err = store.ReadSourceJSONAsync(ctx)
				} else {
					raw, err = store.ReadSourceJSON(ctx)
				}
				if err != nil {
					t.Fatal(err)
				}
				values = append(values, raw)
			}
			read(false)
			directory, service = row.PhysicalTarget, row.TargetService
			if row.Username {
				user, userPresent = "", row.TargetUser != nil
				if row.TargetUser != nil {
					user = *row.TargetUser
				}
			}
			mkdirDirectory = directory
			if row.Target != nil && *row.Target == "" {
				mkdirDirectory = ""
			}
			read(false)
			read(true)
			now = now.Add(31000 * time.Millisecond)
			read(true)
			store.Clear()
			read(false)
			result := store.UpdateSourceJSON(ctx, row.Update)
			resultRaw, _ := json.Marshal(map[string]any{"success": result.Success})
			if result.Warning != "" {
				resultRaw, _ = json.Marshal(map[string]any{"success": result.Success, "warning": result.Warning})
			}
			values = append(values, resultRaw)
			read(true)
			deleted, _ := json.Marshal(store.Delete(ctx))
			values = append(values, deleted)
			read(true)
			if len(values) != len(row.Values) {
				t.Fatal("original consumer stage count", len(values), len(row.Values))
			}
			for i, raw := range values {
				var got, want any
				if json.Unmarshal(raw, &got) != nil || json.Unmarshal(row.Values[i], &want) != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("stage%d got %s Source %s", i, raw, row.Values[i])
				}
			}
			if !reflect.DeepEqual(calls, row.Calls) {
				t.Errorf("actual external identity and cache calls got %+v Source %+v", calls, row.Calls)
			}
			wantPrimary := map[string]string{}
			for _, pair := range row.Primary {
				wantPrimary[pair[0]] = pair[1]
			}
			if !reflect.DeepEqual(primary, wantPrimary) {
				t.Error("physical private keychain entries differ", primary, wantPrimary)
			}
			for path, want := range map[string]*string{filepath.Join(row.Initial, ".credentials.json"): row.InitialDisk, filepath.Join(row.PhysicalTarget, ".credentials.json"): row.TargetDisk} {
				raw, err := os.ReadFile(path)
				if want == nil {
					if !os.IsNotExist(err) {
						t.Error("original deleted plaintext still exists", path, err)
					}
				} else if err != nil || string(raw) != *want {
					t.Error("physical private plaintext differs", path, string(raw), *want, err)
				}
			}
		})
	}
}
