package pluginzip

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type seedItem struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Data   string `json:"data,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	Target string `json:"target,omitempty"`
}

func seededFile(path, data string, mode uint32) seedItem {
	return seedItem{Path: path, Kind: "file", Data: base64.StdEncoding.EncodeToString([]byte(data)), Mode: mode}
}
func seed(t *testing.T, root string, items []seedItem) {
	t.Helper()
	if err := os.MkdirAll(root, 0777); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		path := filepath.Join(root, filepath.FromSlash(item.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			t.Fatal(err)
		}
		var err error
		switch item.Kind {
		case "directory":
			err = os.MkdirAll(path, 0777)
		case "symlink":
			err = os.Symlink(item.Target, path)
		default:
			data, _ := base64.StdEncoding.DecodeString(item.Data)
			err = os.WriteFile(path, data, 0666)
			if err == nil && item.Mode != 0 {
				err = os.Chmod(path, os.FileMode(item.Mode))
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
func tree(t *testing.T, root string) []any {
	t.Helper()
	result := []any{}
	var walk func(string)
	walk = func(relative string) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			if relative != "" {
				result = append(result, map[string]any{"path": relative, "kind": "directory", "mode": uint32(info.Mode().Perm())})
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				name := entry.Name()
				if relative != "" {
					name = relative + "/" + name
				}
				walk(name)
			}
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, map[string]any{"path": relative, "kind": "file", "mode": uint32(info.Mode().Perm()), "data": base64.StdEncoding.EncodeToString(data)})
		}
	}
	walk("")
	return result
}
func content(entries []Entry) []any {
	result := []any{}
	for _, entry := range entries {
		result = append(result, map[string]any{"name": entry.Name, "data": base64.StdEncoding.EncodeToString(entry.Data)})
	}
	return result
}
func zipBytes(t *testing.T, entries []Entry, method uint16) []byte {
	t.Helper()
	var b bytes.Buffer
	writer := zip.NewWriter(&b)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.Name, Method: method, CreatorVersion: 3<<8 | 20, ExternalAttrs: uint32(entry.Mode) << 16}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(entry.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func mustService(t *testing.T, home string, before func(Operation) error) *Service {
	t.Helper()
	service, err := New(Options{TempDir: filepath.Join(home, "tmp"), Home: home, Now: func() time.Time { return time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC) }, Before: before})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	return service
}
func errorRecord(err error) map[string]any {
	var code any
	var z *ZipError
	var io *IOError
	if errors.As(err, &z) {
		code = z.Code
	}
	if errors.As(err, &io) {
		code = io.Code
	}
	return map[string]any{"error": err.Error(), "code": code}
}
func marshal(v any) []byte { b, _ := json.Marshal(v); return b }
func equalJSON(t *testing.T, actual, expected []byte) {
	t.Helper()
	var a, b any
	if json.Unmarshal(actual, &a) != nil || json.Unmarshal(expected, &b) != nil {
		t.Fatalf("invalid JSON: %s", bounded(string(expected)))
	}
	if reflect.DeepEqual(a, b) {
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		for key, v := range am {
			if reflect.DeepEqual(v, bm[key]) {
				continue
			}
			if al, ok := v.([]any); ok {
				bl, _ := bm[key].([]any)
				for i, item := range al {
					if i >= len(bl) {
						t.Errorf("%s[%d] Source missing", key, i)
					} else if !reflect.DeepEqual(item, bl[i]) {
						t.Errorf("%s[%d] Go=%s Source=%s", key, i, bounded(string(marshal(item))), bounded(string(marshal(bl[i]))))
					}
				}
			} else {
				t.Errorf("%s Go=%s Source=%s", key, bounded(string(marshal(v))), bounded(string(marshal(bm[key]))))
			}
		}
		return
	}
	t.Errorf("Go=%s Source=%s", bounded(string(actual)), bounded(string(expected)))
}
func bounded(s string) string {
	if len(s) > 1400 {
		return s[:1400] + "…"
	}
	return s
}

func TestArchivesLifecycleAndValidationMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	service := mustService(t, home, nil)
	archiveSeeds := [][]Entry{
		{{Name: "script", Data: []byte("#!/bin/sh\necho actual\n"), Mode: 0100755}, {Name: "readonly", Data: []byte("content"), Mode: 0100444}, {Name: "empty/", Mode: 0040750}, {Name: "nested/a", Data: []byte("A"), Mode: 0100640}},
		{{Name: "dup", Data: []byte("first"), Mode: 0100751}, {Name: "2", Data: []byte("two")}, {Name: "10", Data: []byte("ten")}, {Name: "1", Data: []byte("one")}, {Name: "dup", Data: []byte("last"), Mode: 0100644}},
		{{Name: "__proto__", Data: []byte("ignored")}, {Name: "constructor", Data: []byte("constructor")}},
		{{Name: "../escape", Data: []byte("bad")}}, {{Name: `..\escape`, Data: []byte("bad")}}, {{Name: "/absolute", Data: []byte("bad")}},
		{{Name: "中文/脚本", Data: []byte("utf8"), Mode: 0100755}},
		{{Name: "as-file/", Mode: 0040755}},
	}
	archives := [][]byte{}
	for _, entries := range archiveSeeds {
		archives = append(archives, zipBytes(t, entries, zip.Store))
	}
	archives = append(archives, []byte("bad"), []byte{}, zipBytes(t, []Entry{}, zip.Store))
	badCompression := append([]byte(nil), archives[0]...)
	central := int(u32(badCompression, eocd(badCompression)+16))
	binary.LittleEndian.PutUint16(badCompression[central+10:], 99)
	archives = append(archives, badCompression)
	badCRC := append([]byte(nil), archives[0]...)
	binary.LittleEndian.PutUint32(badCRC[central+16:], 42)
	archives = append(archives, badCRC)
	large := append([]byte(nil), archives[0]...)
	binary.LittleEndian.PutUint32(large[central+24:], MaxFileSize+1)
	archives = append(archives, large)
	for _, payload := range [][]byte{{0x07}, {0x01, 0x01, 0x00, 0x00, 0x00, 'A'}, {0x03, 0x02}, {0x03}} {
		raw := zipBytes(t, []Entry{{Name: "raw", Data: payload}}, zip.Store)
		central := int(u32(raw, eocd(raw)+16))
		binary.LittleEndian.PutUint16(raw[central+10:], 8)
		binary.LittleEndian.PutUint32(raw[central+24:], 1)
		archives = append(archives, raw)
	}
	latin := zipBytes(t, []Entry{{Name: string([]byte{'c', 'a', 'f', 0xe9}), Data: []byte("latin1"), Mode: 0100755}}, zip.Store)
	archives = append(archives, latin)
	archiveInput := []any{}
	actualArchives := []any{}
	for i, data := range archives {
		root := filepath.Join(home, "go-archives", fmt.Sprint(i))
		path := root + ".zip"
		_ = os.MkdirAll(filepath.Dir(path), 0777)
		_ = os.WriteFile(path, data, 0666)
		initial := []seedItem{}
		if i == 0 {
			initial = []seedItem{seededFile("stale", "keep", 0640), seededFile("readonly", "old", 0600)}
			seed(t, root, initial)
		}
		if i == 7 {
			initial = []seedItem{seededFile("as-file", "keep-directory-conflict", 0640)}
			seed(t, root, initial)
		}
		archiveInput = append(archiveInput, map[string]any{"data": base64.StdEncoding.EncodeToString(data), "seed": initial})
		entries, err := Unzip(data, "darwin")
		if err == nil {
			err = service.Extract(context.Background(), path, root)
		}
		if err != nil {
			result := errorRecord(err)
			result["error"] = strings.ReplaceAll(err.Error(), filepath.Join(home, "go-archives"), "$ARCHIVES")
			result["tree"] = tree(t, root)
			actualArchives = append(actualArchives, result)
		} else {
			actualArchives = append(actualArchives, map[string]any{"entries": content(entries), "modes": ParseModes(data), "tree": tree(t, root)})
		}
	}
	createSeeds := [][]seedItem{
		{seededFile("script.sh", "#!/bin/sh\necho actual\n", 0755), seededFile("10", "ten", 0644), seededFile("2", "two", 0644), seededFile(".git/hidden", "must-not-zip", 0644), seededFile(".claude-plugin/plugin.json", `{"name":"actual"}`, 0644), {Path: "empty", Kind: "directory"}, {Path: "linked-file", Kind: "symlink", Target: "script.sh"}, {Path: "linked-dir", Kind: "symlink", Target: ".claude-plugin"}, {Path: "broken", Kind: "symlink", Target: "absent"}, {Path: "cycle", Kind: "symlink", Target: "."}},
		{}, {seededFile("nested/中文.txt", "actual unicode", 0640)},
		{seededFile("__proto__", "prototype tuple data", 0755)},
	}
	var dynamic strings.Builder
	for i := 0; i < 800; i++ {
		fmt.Fprintf(&dynamic, "record %d: actual source comparison / segment-%d / checksum-%08x\n", i, i%97, uint32(i)*2654435761)
	}
	createSeeds = append(createSeeds, []seedItem{seededFile("dynamic.txt", dynamic.String(), 0644)})
	createInput := []any{}
	actualCreated := []any{}
	for i, items := range createSeeds {
		createInput = append(createInput, map[string]any{"seed": items})
		source := filepath.Join(home, "go-create", fmt.Sprint(i), "source")
		seed(t, source, items)
		data, err := service.Create(context.Background(), source)
		if err != nil {
			actualCreated = append(actualCreated, errorRecord(err))
			continue
		}
		entries, err := Unzip(data, "darwin")
		if err != nil {
			t.Fatal(err)
		}
		path := source + ".zip"
		target := filepath.Join(filepath.Dir(source), "target")
		_ = os.WriteFile(path, data, 0666)
		if err = service.Extract(context.Background(), path, target); err != nil {
			t.Fatal(err)
		}
		actualCreated = append(actualCreated, map[string]any{"entries": content(entries), "modes": ParseModes(data), "tree": tree(t, target)})
	}
	validations := []any{}
	actualValidations := []any{}
	for _, item := range []struct {
		Name              string
		Size              float64
		Count             int
		Total, Compressed float64
	}{{"safe", 1, 0, 0, 1}, {"../bad", 1, 100000, 0, 100}, {"large", MaxFileSize + 1, 0, 0, 2e9}, {"large", MaxTotalSize + 1, 0, 0, 2e9}, {"../bad", MaxFileSize + 1, 100000, MaxTotalSize, 1}, {"zero", 1, 0, 0, 0}, {"0", 0, 0, 0, 0}} {
		state := ValidationState{FileCount: item.Count, TotalUncompressedSize: item.Total, CompressedSize: item.Compressed}
		validations = append(validations, map[string]any{"file": map[string]any{"name": item.Name, "originalSize": item.Size}, "state": map[string]any{"fileCount": item.Count, "totalUncompressedSize": item.Total, "compressedSize": item.Compressed}})
		err := ValidateFile(Metadata{item.Name, item.Size}, &state, "darwin")
		result := map[string]any{"isValid": err == nil}
		if err != nil {
			result["error"] = err.Error()
		}
		actualValidations = append(actualValidations, map[string]any{"result": result, "state": map[string]any{"fileCount": state.FileCount, "totalUncompressedSize": state.TotalUncompressedSize, "compressedSize": state.CompressedSize, "errors": []any{}}})
	}
	paths := []string{"a", "../bad", `..\bad`, "a/../b", "/absolute", "C:\\file", "./safe", "", "safe..", "a/.../b", "a/..", "~/.claude", "a\\..\\x"}
	pathValues := []bool{}
	for _, path := range paths {
		pathValues = append(pathValues, IsPathSafe(path, "darwin"))
	}
	names := []string{"official-market", "name.space", "🧠 中文", "../bad", "a@b"}
	nameValues := []string{}
	for _, name := range names {
		nameValues = append(nameValues, MarketplaceRelativePath(name))
	}
	sources := []string{"github", "git", "url", "settings", "file", "directory", "npm"}
	sourceValues := []bool{}
	for _, source := range sources {
		sourceValues = append(sourceValues, SupportedMarketplaceSource(source))
	}
	envInput := []any{}
	actualEnv := []any{}
	for _, item := range []struct {
		Enabled string
		Path    *string
	}{{"1", strptr("~/mounted")}, {"TRUE", strptr("relative")}, {"off", strptr("/mounted")}, {"yes", nil}, {"on", strptr("")}} {
		envInput = append(envInput, map[string]any{"enabled": item.Enabled, "path": item.Path})
		env := map[string]string{"CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE": item.Enabled}
		if item.Path != nil {
			env["CLAUDE_CODE_PLUGIN_CACHE_DIR"] = *item.Path
		}
		configured, _ := New(Options{TempDir: filepath.Join(home, "tmp"), Home: home, Environment: env})
		var path any
		if configured.CachePath() != "" {
			path = configured.CachePath()
		}
		value := map[string]any{"enabled": configured.CacheEnabled(), "path": path}
		for key, file := range map[string]string{"known": "known_marketplaces.json", "installed": "installed_plugins.json", "marketplaces": "marketplaces", "plugins": "plugins"} {
			p, err := configured.CacheFile(file)
			if err != nil {
				value["error"] = err.Error()
				break
			}
			value[key] = p
		}
		actualEnv = append(actualEnv, value)
	}
	first, err := service.SessionPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	one := true
	for i := 0; i < 20; i++ {
		next, _ := service.SessionPath(context.Background())
		one = one && next == first
	}
	_ = service.Cleanup(context.Background())
	_, err = os.Stat(first)
	removed := errors.Is(err, os.ErrNotExist)
	next, _ := service.SessionPath(context.Background())
	fresh := next != first
	_ = service.Cleanup(context.Background())
	convert := filepath.Join(home, "go-convert", "source")
	seed(t, convert, []seedItem{seededFile("actual", "retained", 0755)})
	if err = service.Convert(context.Background(), convert, convert+".zip"); err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(convert)
	converted, _ := service.ReadAndUnzip(context.Background(), convert+".zip")
	failedSource := filepath.Join(home, "go-failed-convert", "source")
	seed(t, failedSource, []seedItem{seededFile("actual", "must-remain", 0755)})
	_ = os.MkdirAll(failedSource+".zip", 0777)
	failure := service.Convert(context.Background(), failedSource, failedSource+".zip")
	var failureCode any
	if failure != nil {
		var io *IOError
		if errors.As(failure, &io) {
			failureCode = io.Code
		}
	}
	siblings, _ := os.ReadDir(filepath.Dir(failedSource))
	siblingNames := []string{}
	for _, item := range siblings {
		siblingNames = append(siblingNames, item.Name())
	}
	failedConvert := map[string]any{"code": failureCode, "sourceTree": tree(t, failedSource), "siblings": siblingNames}
	atomicPath := filepath.Join(home, "go-atomic", "owned.json")
	_ = os.MkdirAll(filepath.Dir(atomicPath), 0777)
	_ = os.WriteFile(atomicPath, []byte("old"), 0600)
	_ = os.Chmod(atomicPath, 0600)
	if err := service.AtomicWrite(context.Background(), atomicPath, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(atomicPath)
	siblings, _ = os.ReadDir(filepath.Dir(atomicPath))
	siblingNames = []string{}
	for _, item := range siblings {
		siblingNames = append(siblingNames, item.Name())
	}
	atomic := map[string]any{"data": "replacement", "mode": uint32(info.Mode().Perm()), "siblings": siblingNames}
	actual := map[string]any{"archives": actualArchives, "created": actualCreated, "validations": actualValidations, "paths": pathValues, "names": nameValues, "supported": sourceValues, "environment": actualEnv, "session": map[string]any{"oneSession": one, "removed": removed, "freshSession": fresh}, "convert": map[string]any{"sourceRemoved": errors.Is(err, os.ErrNotExist), "entries": content(converted)}}
	actual["failedConvert"] = failedConvert
	actual["atomic"] = atomic
	input := map[string]any{"clock": "2026-10-06T01:02:03.000Z", "archives": archiveInput, "create": createInput, "validations": validations, "paths": paths, "names": names, "sources": sources, "environment": envInput}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-zip.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_PLUGIN_ZIP_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(marshal(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source: %v %s", err, bounded(stderr.String()))
	}
	var expected map[string]any
	if err = json.Unmarshal(output, &expected); err != nil {
		t.Fatalf("Source JSON: %v %s", err, bounded(string(output)))
	}
	for _, item := range expected["created"].([]any) {
		record := item.(map[string]any)
		if encoded, ok := record["archive"].(string); ok {
			data, _ := base64.StdEncoding.DecodeString(encoded)
			entries, err := Unzip(data, "darwin")
			if err != nil {
				t.Fatal(err)
			}
			equalJSON(t, marshal(content(entries)), marshal(record["entries"]))
			delete(record, "archive")
		}
	}
	equalJSON(t, marshal(actual), marshal(expected))
}
func strptr(s string) *string { return &s }
