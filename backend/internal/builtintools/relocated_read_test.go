package builtintools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualReadAndPermissionUseMovedAttachmentAndRejectBrokenMap(t *testing.T) {
	service, owner, home, _ := fixture(t)
	oldRoot := filepath.Join(home, "old-config")
	config := filepath.Join(home, "moved-config")
	service.options.Environment["CLAUDE_CONFIG_DIR"] = config
	oldPath := filepath.Join(oldRoot, "im-downloads", "saved-session", "report.txt")
	moved := filepath.Join(config, "im-downloads", "saved-session", "report.txt")
	metadata := filepath.Join(config, "cc-haha", "storage-relocations.json")
	for _, path := range []string{oldPath, moved, metadata} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(oldPath, []byte("STALE_ORIGINAL"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moved, []byte("REAL_MOVED_CONTENT"), 0600); err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(map[string]any{"version": 1, "previousRoots": []string{oldRoot}})
	if err := os.WriteFile(metadata, document, 0600); err != nil {
		t.Fatal(err)
	}
	in := protocol.NewJSONObject(field("file_path", oldPath))
	raw := call(t, service, owner, "Read", in)
	mapped, err := MapResult("Read", raw, quote("actual-old-reference"))
	if err != nil || !strings.Contains(mapped.String("content"), "REAL_MOVED_CONTENT") || strings.Contains(mapped.String("content"), "STALE_ORIGINAL") {
		t.Fatal("Read revived old attachment", string(raw), err)
	}
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		path, e := service.toolPath("Read", in)
		if e != nil {
			return e
		}
		if path != moved {
			t.Fatal("permission path differs from actual reader", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(metadata, []byte(`{"version":1,"previousRoots":["relative"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := service.Permission(scope, "Read", in); e == nil {
			t.Fatal("permission ignored broken metadata")
		}
		if _, e := service.Call(context.Background(), "Read", in, scope, false); e == nil {
			t.Fatal("reader ignored broken metadata")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(oldPath)
	if string(old) != "STALE_ORIGINAL" {
		t.Fatal("old attachment mutated")
	}
}
