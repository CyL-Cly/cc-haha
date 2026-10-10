package tlsconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
)

func TestExplicitReaderAndProcessMemo(t *testing.T) {
	if _, err := NewState(nil); err == nil {
		t.Fatal("missing host reader accepted")
	}
	reads := 0
	state, _ := NewState(func(string) ([]byte, error) { reads++; return []byte("first"), nil })
	first, err := state.Certificates(nil)
	if err != nil || first != nil {
		t.Fatal(first, err)
	}
	if got, err := state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "original"}); err != nil || got != nil || reads != 0 {
		t.Fatal("undefined was not cached", reads, got, err)
	}
	state.Clear()
	first, err = state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "original"})
	if err != nil || len(first.Items) != 146 || reads != 1 {
		t.Fatal(first, reads, err)
	}
	first.Items = append(first.Items, json.RawMessage(`"external\ud800"`))
	next, err := state.Certificates(map[string]string{"NODE_OPTIONS": "--use-system-ca"})
	if err != nil || next != first || len(next.Items) != 147 || reads != 1 {
		t.Fatal("Source shared reference/memo changed", err)
	}
	state.Clear()
	if _, err = state.Certificates(map[string]string{"NODE_OPTIONS": "--use-system-ca"}); err == nil {
		t.Fatal("fresh system store read must be explicit capability")
	}
	if _, err = state.Certificates(map[string]string{"NODE_OPTIONS": "--use-openssl-ca"}); err == nil {
		t.Fatal("fresh openssl store read must be explicit capability")
	}
	if next, err = state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "original"}); err != nil || next == first || len(next.Items) != 146 || reads != 2 {
		t.Fatal("failed capability incorrectly memoized", reads, err)
	}
}
func TestFailedReadAndUTF8Preservation(t *testing.T) {
	state, _ := NewState(func(string) ([]byte, error) { return nil, errors.New("original read failed") })
	value, err := state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "private-file"})
	if err != nil || len(value.Items) != 145 {
		t.Fatal(value, err)
	}
	expected := `"\ufeff😀���\u0000"`
	state, _ = NewState(func(string) ([]byte, error) {
		return []byte{0xef, 0xbb, 0xbf, 0xf0, 0x9f, 0x98, 0x80, 0xed, 0xa0, 0x80, 0}, nil
	})
	value, err = state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "private-file"})
	if err != nil {
		t.Fatal(err)
	}
	// The JavaScript JSON stringifier retains the literal BOM, not an escaped
	// codepoint. Decode the expected JSON to produce that same independent value.
	var want string
	_ = json.Unmarshal([]byte(expected), &want)
	raw, _ := json.Marshal(want)
	if string(value.Items[145]) != string(raw) {
		t.Fatalf("replacement/BOM changed: %s != %s", value.Items[145], raw)
	}
}
func TestPinnedRuntimeStringsAndResourceIsolation(t *testing.T) {
	roots := BundledCertificates()
	if len(roots.Items) != 145 {
		t.Fatal(len(roots.Items))
	}
	data := []byte{}
	for _, raw := range roots.Items {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if value[len(value)-1] == '\n' {
			t.Fatal("inferred per-item final newline")
		}
		data = append(data, []byte(value)...)
		data = append(data, '\n')
	}
	sha := sha256.Sum256(data)
	if hex.EncodeToString(sha[:]) != "e3569ed5bde6e59d7718cd962b1f07a95ab0a4226a9fc35a18d8f586f98ea6a4" {
		t.Fatal("pinned bundle was replaced", hex.EncodeToString(sha[:]))
	}
	roots.Items[0] = json.RawMessage(`"changed"`)
	if string(BundledCertificates().Items[0]) == `"changed"` {
		t.Fatal("mutable cache corrupted runtime resource")
	}
}
func TestConcurrentMemoOnlyReadsOnce(t *testing.T) {
	reads := 0
	state, _ := NewState(func(string) ([]byte, error) { reads++; return []byte("CA"), nil })
	var wait sync.WaitGroup
	values := make([]*CertificateArray, 24)
	for i := range values {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			values[index], _ = state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "original"})
		}(i)
	}
	wait.Wait()
	if reads != 1 {
		t.Fatal(reads)
	}
	for _, value := range values {
		if value != values[0] {
			t.Fatal("process memo allocated another reference")
		}
	}
}
func TestActualSourceCAStateGolden(t *testing.T) {
	var contract struct {
		Rows         []originalCase    `json:"rows"`
		Observations []json.RawMessage `json:"observations"`
	}
	data, err := os.ReadFile("testdata/tls_config_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Rows) != 30 || len(contract.Rows) != len(contract.Observations) {
		t.Fatal("Source corpus changed without contract", len(contract.Rows))
	}
	for index, row := range contract.Rows {
		t.Run(row.Name, func(t *testing.T) {
			observed, err := observe(row, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(observed)
			if err != nil {
				t.Fatal(err)
			}
			var actual, want any
			_ = json.Unmarshal(raw, &actual)
			_ = json.Unmarshal(contract.Observations[index], &want)
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				t.Fatalf("actual original Source cache differed\n%s\n%s", a, b)
			}
		})
	}
}
