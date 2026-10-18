// Package gochildcoverage supplies instrumentation only to process-test adapters.
// No production package imports it. All fixture bytes remain source-snapshotted.
package gochildcoverage

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
)

type publicGoCoverageManifest struct {
	SchemaVersion    int      `json:"schemaVersion"`
	RootDir          string   `json:"rootDir"`
	ModulePath       string   `json:"modulePath"`
	CoveragePackages []string `json:"coveragePackages"`
	SourceSnapshot   []struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
	} `json:"sourceSnapshot"`
}

var publicGoCoverageBuilds sync.Map        // absolute output path -> actual instrumented build arguments
var publicGoCoverageBuiltBinaries sync.Map // SHA256 of a completed build -> its original arguments

var publicGoCoverageState struct {
	sync.Once
	manifest     publicGoCoverageManifest
	sink, digest string
	err          error
}

// Go gives parent tests both this flag and GOCOVERDIR. A reexecuted helper's
// m.Run otherwise invents a different temporary directory, even when it has
// inherited GOCOVERDIR. Direct os.Exit helpers use Go's ordinary exit hooks.
func Main(m *testing.M) {
	Align()
	os.Exit(m.Run())
}

func Align() {
	flag.Parse()
	if testing.CoverMode() == "atomic" && os.Getenv("GOCOVERDIR") != "" {
		if err := flag.Set("test.gocoverdir", os.Getenv("GOCOVERDIR")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func publicGoCoverage(t *testing.T) (publicGoCoverageManifest, string, string) {
	t.Helper()
	output := flag.Lookup("test.outputdir")
	if testing.CoverMode() != "atomic" || output == nil || output.Value.String() == "" {
		return publicGoCoverageManifest{}, "", ""
	}
	publicGoCoverageState.Do(func() {
		sink, err := filepath.EvalSymlinks(output.Value.String())
		if err != nil {
			publicGoCoverageState.err = err
			return
		}
		path := filepath.Join(sink, "manifest.json")
		bytes, err := os.ReadFile(path)
		// An unrelated ordinary go test -outputdir is not a collector opt-in.
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			publicGoCoverageState.err = err
			return
		}
		var manifest publicGoCoverageManifest
		if err = json.Unmarshal(bytes, &manifest); err != nil || manifest.SchemaVersion != 1 || !filepath.IsAbs(manifest.RootDir) || manifest.ModulePath == "" || len(manifest.CoveragePackages) == 0 || len(manifest.SourceSnapshot) == 0 {
			publicGoCoverageState.err = fmt.Errorf("invalid public Go coverage manifest: %v", err)
			return
		}
		cwd, err := os.Getwd()
		if err != nil {
			publicGoCoverageState.err = err
			return
		}
		cwd, err = filepath.EvalSymlinks(cwd)
		if err != nil || !strings.HasPrefix(cwd, manifest.RootDir+string(filepath.Separator)) {
			publicGoCoverageState.err = fmt.Errorf("coverage caller outside frozen source root")
			return
		}
		for _, row := range manifest.SourceSnapshot {
			path := filepath.Join(manifest.RootDir, filepath.FromSlash(row.File))
			rel, err := filepath.Rel(manifest.RootDir, path)
			if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				publicGoCoverageState.err = fmt.Errorf("invalid coverage source path %q", row.File)
				return
			}
			data, err := os.ReadFile(path)
			sum := sha256.Sum256(data)
			if err != nil || hex.EncodeToString(sum[:]) != row.SHA256 {
				publicGoCoverageState.err = fmt.Errorf("coverage source SHA changed: %s: %v", row.File, err)
				return
			}
		}
		sum := sha256.Sum256(bytes)
		publicGoCoverageState.manifest, publicGoCoverageState.sink, publicGoCoverageState.digest = manifest, sink, hex.EncodeToString(sum[:])
	})
	if publicGoCoverageState.err != nil {
		t.Fatal(publicGoCoverageState.err)
	}
	return publicGoCoverageState.manifest, publicGoCoverageState.sink, publicGoCoverageState.digest
}

// RegisterBuilt is only needed when the existing fixture relocates its output
// before first execution. Capture has already instrumented this exact command;
// require its successful physical completion before certifying the bytes.
func RegisterBuilt(t *testing.T, command *exec.Cmd) {
	t.Helper()
	_, sink, _ := publicGoCoverage(t)
	if sink == "" {
		return
	}
	if command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatal("relocated public Go child requires an actually completed successful build")
	}
	for i, arg := range command.Args {
		if arg != "-o" || i+1 >= len(command.Args) {
			continue
		}
		path := command.Args[i+1]
		if !filepath.IsAbs(path) {
			cwd := command.Dir
			if cwd == "" {
				cwd, _ = os.Getwd()
			}
			path = filepath.Join(cwd, path)
		}
		original, known := publicGoCoverageBuilds.Load(filepath.Clean(path))
		if !known || strings.Join(original.([]string), "\x00") != strings.Join(command.Args, "\x00") {
			t.Fatal("relocated public Go child has no matching original instrumented build")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := buildinfo.ReadFile(path); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		publicGoCoverageBuiltBinaries.Store(hex.EncodeToString(sum[:]), append([]string{}, command.Args...))
		return
	}
	t.Fatal("relocated public Go child build has no explicit output")
}

func publicGoBuildArguments(path string) ([]string, bool) {
	if original, known := publicGoCoverageBuilds.Load(filepath.Clean(path)); known {
		return original.([]string), true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	sum := sha256.Sum256(data)
	original, known := publicGoCoverageBuiltBinaries.Load(hex.EncodeToString(sum[:]))
	if !known {
		return nil, false
	}
	return original.([]string), true
}

// IndirectEnvironment binds an actual retained public CLI to the same strict
// collector when an existing fixture executes it through tmux rather than an
// exec.Cmd in this process. It does not start a process or certify execution;
// only the child's real runtime counters can establish that later.
func IndirectEnvironment(t *testing.T, binary string) string {
	t.Helper()
	_, sink, _ := publicGoCoverage(t)
	if sink == "" {
		return ""
	}
	path, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, known := publicGoBuildArguments(path); !known {
		t.Fatal("indirect public Go child was not built against this frozen coverage manifest")
	}
	command := exec.Command(path)
	Capture(t, command)
	for _, value := range command.Env {
		if directory, ok := strings.CutPrefix(value, "GOCOVERDIR="); ok && directory != "" {
			return directory
		}
	}
	t.Fatal("indirect public Go child has no attested coverage environment")
	return ""
}

// Call immediately before the existing command's Run/Start/Output. It changes
// only instrumentation on recognized public Go builds and coverage env on an
// actual Go executable (possibly an argument of the existing Python driver).
func Capture(t *testing.T, command *exec.Cmd) {
	t.Helper()
	manifest, sink, digest := publicGoCoverage(t)
	if sink == "" {
		return
	}
	args := command.Args
	if len(args) > 2 && args[1] == "build" && (filepath.Base(args[0]) == "go" || filepath.Base(args[0]) == "go.exe") {
		last := args[len(args)-1]
		cwd := command.Dir
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		if last != "../../cmd/claude-haha" && last != "./cmd/claude-haha" && !strings.Contains(last, "/testdata/") && !(last == "." && filepath.Base(cwd) == "claude-haha") {
			return
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "-cover") {
				t.Fatal("public CLI builder already has unreviewed coverage flags")
			}
		}
		// ./... belongs to the parent runner's backend cwd. Resolve that one
		// pattern to its module identity before using the unchanged scope from
		// an existing cmd/application fixture's different working directory.
		packages := append([]string{}, manifest.CoveragePackages...)
		for i, pattern := range packages {
			if pattern == "./..." {
				packages[i] = manifest.ModulePath + "/..."
			}
		}
		command.Args = append(append([]string{}, args[:2]...), append([]string{"-cover", "-covermode=atomic", "-coverpkg=" + strings.Join(packages, ",")}, args[2:]...)...)

		for i, arg := range args {
			if arg == "-o" && i+1 < len(args) {
				path := args[i+1]
				if !filepath.IsAbs(path) {
					path = filepath.Join(cwd, path)
				}
				publicGoCoverageBuilds.Store(filepath.Clean(path), append([]string{}, command.Args...))
				if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
					publicGoCoverageBuilds.Store(filepath.Join(parent, filepath.Base(path)), append([]string{}, command.Args...))
				}
			}
		}
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal(err)
	}
	var binary, kind string
	var buildArguments []string
	var additional []map[string]any
	for i, arg := range args {
		if i > 0 && !strings.HasPrefix(filepath.Base(command.Path), "python") {
			continue
		}
		path, err := filepath.Abs(arg)
		if err != nil {
			continue
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		if path == self {
			binary, kind = path, "self-test"
			break
		}
		if filepath.Base(path) == "go" || filepath.Base(path) == "go.exe" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if info, err := readPublicGoBinaryInfo(path); err == nil {
			settings := map[string]string{}
			for _, setting := range info.Settings {
				settings[setting.Key] = setting.Value
			}
			if info.Path == manifest.ModulePath || strings.HasPrefix(info.Path, manifest.ModulePath+"/") {
				if settings["-cover"] != "true" {
					t.Fatal("public CLI is missing actual -cover instrumentation")
				}
				original, known := publicGoBuildArguments(path)
				if !known {
					t.Fatal("public Go child was not built against this frozen coverage manifest")
				}
				buildArguments = original
				binary, kind = path, "fresh-cli"
				break
			}
		}
	}
	if binary == "" {
		return
	}
	for _, value := range command.Env {
		_, path, ok := strings.Cut(value, "=")
		if !ok || !filepath.IsAbs(path) || path == binary {
			continue
		}
		original, known := publicGoBuildArguments(path)
		if !known {
			continue
		}
		if entry := retainPublicGoBinary(t, sink, path, "fresh-cli"); entry != nil {
			entry["buildArguments"] = original
			additional = append(additional, entry)
		}
	}
	primary := retainPublicGoBinary(t, sink, binary, kind)
	if primary == nil {
		t.Fatal("unrecognized public Go child")
	}
	binaryHash := primary["binarySha256"].(string)
	if kind == "fresh-cli" {
		primary["buildArguments"] = buildArguments
		publicGoCoverageBuiltBinaries.Store(binaryHash, append([]string{}, buildArguments...))
	}
	rawDir, err := os.MkdirTemp(filepath.Join(sink, "raw"), "child-")
	if err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"schemaVersion": 1, "manifestSha256": digest, "kind": kind, "binary": "bin/" + binaryHash, "binarySha256": binaryHash, "test": t.Name(), "additionalBinaries": additional}
	if kind == "fresh-cli" {
		receipt["buildArguments"] = buildArguments
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(rawDir, "receipt.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	environment := command.Env
	if environment == nil {
		environment = os.Environ()
	}
	filtered := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		if !strings.HasPrefix(value, "GOCOVERDIR=") {
			filtered = append(filtered, value)
		}
	}
	command.Env = append(filtered, "GOCOVERDIR="+rawDir)
}

// Kept separate so the test-only helpers use Go's own executable metadata
// reader on the actual retained binary, including cross-platform executables.
func readPublicGoBinaryInfo(path string) (*debug.BuildInfo, error) { return buildinfo.ReadFile(path) }

func retainPublicGoBinary(t *testing.T, sink, path, kind string) map[string]any {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buildinfo.ReadFile(path); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(bytes)
	binaryHash := hex.EncodeToString(hash[:])
	binDir := filepath.Join(sink, "bin")
	if err = os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(binDir, binaryHash)
	file, err := os.OpenFile(retained, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_, err = file.Write(bytes)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	} else if os.IsExist(err) {
		err = nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"kind": kind, "binary": "bin/" + binaryHash, "binarySha256": binaryHash}
}
