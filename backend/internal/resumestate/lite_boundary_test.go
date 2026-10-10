package resumestate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type nativeLiteFiles struct {
	OSFiles
	mode, path string
	calls      *[]map[string]any
}

func (f nativeLiteFiles) Open(path string) (sessionstore.File, error) {
	if path != f.path {
		return nil, errors.New("only original private path is allowed")
	}
	file, err := f.OSFiles.Open(path)
	if err != nil {
		return nil, err
	}
	return &nativeLiteFile{File: file, mode: f.mode, calls: f.calls}, nil
}

type nativeLiteFile struct {
	sessionstore.File
	mode    string
	ordinal int
	calls   *[]map[string]any
}

func (f *nativeLiteFile) ReadAt(bytes []byte, position int64) (int, error) {
	f.ordinal++
	*f.calls = append(*f.calls, map[string]any{"operation": "read", "ordinal": f.ordinal, "position": position})
	if f.mode == "zero-head" && f.ordinal == 1 {
		return 0, nil
	}
	if f.mode == "tail-read" && f.ordinal == 2 {
		return 0, errors.New("original-private-tail-read-fault")
	}
	return f.File.ReadAt(bytes, position)
}
func (f *nativeLiteFile) Close() error {
	*f.calls = append(*f.calls, map[string]any{"operation": "close"})
	if err := f.File.Close(); err != nil {
		return err
	}
	if f.mode == "close" {
		return errors.New("original-private-close-fault")
	}
	return nil
}
func TestActualSourcePortableFaultsFromIndependentNativeFile(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		HelperContracts []struct {
			Mode, Bytes string
			Expected    json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.HelperContracts) != 3 {
		t.Fatal("explicit actual Source portable helper capture missing")
	}
	for _, row := range data.HelperContracts {
		t.Run(row.Mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			writePrivate(t, path, row.Bytes)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			calls := []map[string]any{}
			head, tail := ReadHeadAndTail(nativeLiteFiles{mode: row.Mode, path: path, calls: &calls}, path, info.Size())
			var expected, actual any
			if err := json.Unmarshal(row.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encode(map[string]any{"head": head, "tail": tail, "calls": calls}), &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("actual portable Source differs: %s vs %s", encode(actual), row.Expected)
			}
			if bytes, err := os.ReadFile(path); err != nil || string(bytes) != row.Bytes {
				t.Fatal("fault modified protected original")
			}
		})
	}
}
