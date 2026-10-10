package providerconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type importFS struct {
	storage.FileSystem
	path          string
	reads, writes int
	fail          bool
}

func (f *importFS) ReadFile(path string) ([]byte, error) {
	if path == f.path {
		f.reads++
	}
	return f.FileSystem.ReadFile(path)
}
func (f *importFS) Rename(from, to string) error {
	if to == f.path {
		f.writes++
		if f.fail {
			return errors.New("private atomic rename rejected")
		}
	}
	return f.FileSystem.Rename(from, to)
}
func TestProviderImportUsesSingleAtomicWriteAndRetainsOriginalBatchOnFailure(t *testing.T) {
	service, _ := fixture(t)
	input := saved("ignored")
	delete(input, "id")
	seed(t, service.indexPath(), indexJSON(saved("old")))
	boundary := &importFS{FileSystem: service.options.Store.FileSystem(), path: service.indexPath()}
	service.options.Store = storage.New(storage.Options{FS: boundary})
	got, e := service.ImportProviders(testContext, []storage.Object{input, input})
	if e != nil || len(got) != 2 || boundary.reads != 1 || boundary.writes != 1 {
		t.Fatal(e, boundary.reads, boundary.writes)
	}
	original, _ := os.ReadFile(service.indexPath())
	boundary.reads = 0
	boundary.writes = 0
	badInput := clone(input)
	badInput["models"] = []byte(`{"main":null}`)
	if _, e = service.ImportProviders(testContext, []storage.Object{input, badInput}); e == nil {
		t.Fatal("late invalid member persisted")
	}
	current, _ := os.ReadFile(service.indexPath())
	if !bytes.Equal(original, current) || boundary.writes != 0 {
		t.Fatal("partial batch persisted")
	}
	boundary.fail = true
	if _, e = service.ImportProviders(testContext, []storage.Object{input}); e == nil {
		t.Fatal("failed rename swallowed")
	}
	current, _ = os.ReadFile(service.indexPath())
	if !bytes.Equal(original, current) {
		t.Fatal("failed atomic replacement lost original batch")
	}
	files, _ := os.ReadDir(filepath.Dir(service.indexPath()))
	for _, file := range files {
		if filepath.Ext(file.Name()) != ".json" {
			t.Fatal("failed temporary file was not removed", file.Name())
		}
	}
	boundary.reads = 0
	boundary.writes = 0
	if got, e = service.ImportProviders(testContext, nil); e != nil || len(got) != 0 || boundary.reads != 0 || boundary.writes != 0 {
		t.Fatal("empty import touched persistence", e)
	}
}
func TestProviderCreationAndBatchAgainstActualOriginalProducer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	cases := []map[string]any{}
	for _, count := range []int{0, 1, 3} {
		for _, single := range []bool{false, true} {
			if single && count != 1 {
				continue
			}
			input := object([]byte(`{"presetId":"custom","name":"Private provider","apiKey":"fake-import-key","baseUrl":"https://private.invalid","models":{"main":" \ufeffm\ud800\ufeff ","haiku":"","sonnet":"s","opus":"o","fable":" \ufeff "},"model1mSupport":{"main":false,"haiku":false,"sonnet":false,"opus":false},"disableExperimentalBetas":false,"supportsNestedToolResultMedia":false,"notes":"retained note"}`))
			inputs := []storage.Object{}
			for i := 0; i < count; i++ {
				v := clone(input)
				if i%2 == 1 {
					v["imageGeneration"] = []byte(`{"model":" \ufeffimg\ud800 ","baseUrl":" https://image.invalid/ ","apiKey":" "}`)
					v["toolSearchEnabled"] = encode(true)
					v["disableExperimentalBetas"] = encode(true)
				}
				inputs = append(inputs, v)
			}
			index := object([]byte(`{"schemaVersion":6,"providers":[],"activeId":null,"providerOrder":["grok-official","claude-official","openai-official"],"futureRoot":{"number":1.000}}`))
			if count == 3 {
				index["providers"] = encode([]storage.Object{saved("existing")})
				index["activeId"] = encode("existing")
				index["providerOrder"] = encode([]string{"grok-official", "existing", "claude-official", "openai-official"})
			}
			cases = append(cases, map[string]any{"index": index, "inputs": inputs, "single": single})
		}
	}
	path, _ := filepath.Abs("../../../../scripts/quality-gate/go-parity/native-provider-import.fixture.ts")
	ctx, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=test", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encode(map[string]any{"home": home, "cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var wants []json.RawMessage
	if json.Unmarshal(out, &wants) != nil || len(wants) != len(cases) {
		t.Fatal(string(out))
	}
	for i, row := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			service, _ := fixture(t)
			seed(t, service.indexPath(), string(encode(row["index"])))
			inputs := row["inputs"].([]storage.Object)
			var got []storage.Object
			var e error
			if row["single"] == true {
				var p storage.Object
				p, e = service.Add(ctx, inputs[0])
				got = []storage.Object{p}
			} else {
				got, e = service.ImportProviders(ctx, inputs)
			}
			if e != nil {
				t.Fatal(e)
			}
			persisted, e := os.ReadFile(service.indexPath())
			if e != nil {
				t.Fatal(e)
			}
			actual := encode(map[string]any{"imported": got, "persisted": json.RawMessage(persisted)})
			for n, p := range got {
				actual = bytes.ReplaceAll(actual, []byte(str(p, "id")), []byte(fmt.Sprintf("new-%d", n)))
			}
			if !orderedJSONEqual(wants[i], actual) {
				t.Fatalf("Source%s Go%s", wants[i], actual)
			}
		})
	}
}
func orderedJSONEqual(a, b json.RawMessage) bool {
	au, ae := protocol.DecodeJavaScriptJSONString(a)
	bu, be := protocol.DecodeJavaScriptJSONString(b)
	if ae == nil || be == nil {
		return ae == nil && be == nil && bytes.Equal(protocol.QuoteJavaScriptUTF16(au), protocol.QuoteJavaScriptUTF16(bu))
	}
	var x, y protocol.JSONObject
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	if x.IsObject() || y.IsObject() {
		if !x.IsObject() || !y.IsObject() || len(x.OwnProperties()) != len(y.OwnProperties()) {
			return false
		}
		for _, p := range x.OwnProperties() {
			found := false
			for _, q := range y.OwnProperties() {
				if orderedJSONEqual(p.NameJSON, q.NameJSON) {
					found = true
					if !orderedJSONEqual(p.Value, q.Value) {
						return false
					}
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if bytes.HasPrefix(bytes.TrimSpace(a), []byte("[")) || bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
		var x, y []json.RawMessage
		if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !orderedJSONEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b))
}
