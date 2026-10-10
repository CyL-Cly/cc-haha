package computeruse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func writeHelperBundle(t *testing.T, bundle string, version string) {
	t.Helper()
	for _, relative := range []string{helperInner, "Contents/Info.plist", "Contents/_CodeSignature/CodeResources"} {
		path := filepath.Join(bundle, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative+version), 0700); err != nil {
			t.Fatal(err)
		}
	}
}
func TestHelperResolverUsesMatchingThinProductAndIgnoresPackagedOverride(t *testing.T) {
	options := fixtureOptions(t)
	options.AppRoot = filepath.Join(options.HomeDir, "Host.app", "Contents", "Resources", "app.asar")
	override := filepath.Join(options.HomeDir, "evil")
	_ = os.WriteFile(override, []byte("evil"), 0700)
	options.Environment["CC_HAHA_CU_HELPER_PATH"] = override
	bundle := filepath.Join(options.HomeDir, "Host.app", "Contents", "Resources", "app.asar.unpacked", "src-tauri", "binaries", helperAppName)
	writeHelperBundle(t, bundle, "new")
	service := fixtureService(t, options)
	if got := service.ResolveSourceHelper(); got != filepath.Join(bundle, filepath.FromSlash(helperInner)) {
		t.Fatalf("packaged override escaped: %s", got)
	}
	for _, item := range []struct{ arch, swift string }{{"arm64", "arm64"}, {"x64", "x86_64"}} {
		got := DevelopmentHelperBinary("/repo", item.arch)
		want := "/repo/native/cu-helper/.build/" + item.swift + "/" + item.swift + "-apple-macosx/release/cc-haha-computer-use.app/Contents/MacOS/cc-haha-computer-use"
		if got != want {
			t.Fatalf("arch=%s got=%s", item.arch, got)
		}
	}
	if DevelopmentHelperBinary("/repo", "ia32") != "" {
		t.Fatal("unsupported architecture guessed a native product")
	}
	if !IsNestedInHostApp(bundle) || IsNestedInHostApp(filepath.Join(options.HomeDir, helperAppName)) {
		t.Fatal("helper TCC subject relocation boundary changed")
	}
}
func TestPackagedHelperActualCopySealValidationRepairAndRollback(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("actual ditto macOS copy primitive")
	}
	options := fixtureOptions(t)
	options.AppRoot = filepath.Join(options.HomeDir, "Host.app", "Contents", "Resources", "app.asar")
	options.Executable = filepath.Join(options.HomeDir, "go-sidecar")
	source := filepath.Join(options.HomeDir, "Host.app", "Contents", "Resources", "app.asar.unpacked", "src-tauri", "binaries", helperAppName)
	writeHelperBundle(t, source, "version1")
	destination := filepath.Join(options.ConfigDir, "cu-helper", helperAppName)
	native := nativeRunner(options)
	var copies atomic.Int32
	var rejectInstalled atomic.Bool
	var rejectSigner atomic.Bool
	options.Run = func(ctx context.Context, file string, args []string, extra []*os.File) (CommandResult, error) {
		if file == "/usr/bin/uname" {
			return CommandResult{Stdout: "25.4.0"}, nil
		}
		if file == "/usr/bin/codesign" {
			target := args[len(args)-1]
			if args[0] == "--verify" {
				if rejectInstalled.Load() && target == destination {
					return CommandResult{Code: 1}, errors.New("fixture installed signature failure")
				}
				return CommandResult{}, nil
			}
			if args[0] == "-dv" {
				id := helperIdentifier
				leafAuthority := "Fixture Development"
				if target == options.Executable {
					id = sidecarIdentifier
					if rejectSigner.Load() {
						leafAuthority = "Another Signer"
					}
				}
				return CommandResult{Stderr: "Identifier=" + id + "\nAuthority=" + leafAuthority + "\nTeamIdentifier=FIXTURE123\n"}, nil
			}
			if len(args) > 1 && strings.HasPrefix(args[1], "--extract-certificates=") {
				prefix := strings.TrimPrefix(args[1], "--extract-certificates=")
				if err := os.WriteFile(prefix+"0", []byte("fixture-leaf-certificate"), 0600); err != nil {
					return CommandResult{Code: 1}, err
				}
				return CommandResult{}, nil
			}
		}
		if file == "/usr/bin/ditto" {
			if len(args) != 3 || args[0] != "--noqtn" {
				return CommandResult{Code: 1}, errors.New("quarantine-preserving copy was requested")
			}
			copies.Add(1)
		}
		return native(ctx, file, args, extra)
	}
	service := fixtureService(t, options)
	ctx := context.Background()
	var group sync.WaitGroup
	for range 5 {
		group.Add(1)
		go func() {
			defer group.Done()
			binary, err := service.ResolveLaunchableHelper(ctx)
			if err != nil || binary != filepath.Join(destination, filepath.FromSlash(helperInner)) {
				t.Errorf("install binary=%s err=%v", binary, err)
			}
		}()
	}
	group.Wait()
	if copies.Load() != 1 {
		t.Fatalf("concurrent install copied %d bundles", copies.Load())
	}
	sourceSeal, _ := bundleFingerprint(source)
	destinationSeal, _ := bundleFingerprint(destination)
	if sourceSeal != destinationSeal {
		t.Fatal("copied signed unit differs from source")
	}
	if err := os.WriteFile(filepath.Join(destination, "Contents", "Info.plist"), []byte("corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveLaunchableHelper(ctx); err != nil {
		t.Fatal(err)
	}
	if copies.Load() != 2 {
		t.Fatal("destination resource corruption escaped revalidation")
	}
	previous := map[string][]byte{}
	for _, relative := range []string{helperInner, "Contents/Info.plist", "Contents/_CodeSignature/CodeResources"} {
		previous[relative], _ = os.ReadFile(filepath.Join(destination, filepath.FromSlash(relative)))
	}
	writeHelperBundle(t, source, "version2")
	rejectInstalled.Store(true)
	if binary, err := service.ResolveLaunchableHelper(ctx); err == nil || binary != "" {
		t.Fatalf("post-replacement failed signature launched %s err=%v", binary, err)
	}
	for relative, want := range previous {
		got, _ := os.ReadFile(filepath.Join(destination, filepath.FromSlash(relative)))
		if !reflect.DeepEqual(got, want) {
			t.Fatal("replacement failure did not restore previous unit")
		}
	}
	rejectInstalled.Store(false)
	rejectSigner.Store(true)
	if binary, err := service.ResolveLaunchableHelper(ctx); err == nil || binary != "" {
		t.Fatalf("mismatched sidecar signer launched %s err=%v", binary, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(destination))
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "staging-") || strings.Contains(entry.Name(), "previous-") || entry.Name() == ".install.lock" {
			t.Fatalf("install residue=%s", entry.Name())
		}
	}
}
