package imregistration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWhatsAppManagedPromotionAndRollbackMatchActualSourcePhysicalFiles(t *testing.T) {
	rows := []map[string]any{{"id": "fresh", "stage": "dir"}, {"id": "replace", "stage": "dir", "target": "dir"}, {"id": "missing", "stage": "missing", "target": "dir"}, {"id": "symlink", "stage": "dir", "target": "symlink"}, {"id": "outside", "stage": "dir", "outside": true}, {"id": "rename-failure", "stage": "dir", "target": "dir", "fail": "rename"}, {"id": "backup-failure", "stage": "dir", "target": "dir", "fail": "remove-backup"}, {"id": "readonly", "stage": "dir", "target": "dir", "readonly": true}}
	raw := waSourceCommand(t, "whatsapp-files.fixture.ts", map[string]any{"rows": rows})
	var source struct {
		Rows         []map[string]any
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(raw, &source); e != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("actual Source %v %s", e, raw)
	}
	for index, row := range rows {
		t.Run(row["id"].(string), func(t *testing.T) {
			config := filepath.Join(t.TempDir(), ".claude")
			s, e := NewWhatsApp(WhatsAppOptions{ConfigDir: config, Login: WhatsAppLoginOptions{UUID: func() string { return row["id"].(string) }}})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			root := s.root()
			_ = os.MkdirAll(root, 0700)
			stage := filepath.Join(root, ".login-"+row["id"].(string))
			target := filepath.Join(root, "target-"+row["id"].(string))
			external := filepath.Join(filepath.Dir(config), "external")
			_ = os.MkdirAll(external, 0700)
			_ = os.WriteFile(filepath.Join(external, "protected"), []byte("KEEP"), 0600)
			if row["stage"] != "missing" {
				_ = os.Mkdir(stage, 0700)
				_ = os.WriteFile(filepath.Join(stage, "new"), []byte("NEW"), 0600)
			}
			if row["target"] == "dir" {
				_ = os.Mkdir(target, 0700)
				_ = os.WriteFile(filepath.Join(target, "old"), []byte("OLD"), 0600)
			}
			if row["target"] == "symlink" {
				if e = os.Symlink(external, target); e != nil {
					t.Fatal(e)
				}
			}
			s.o.Files = waFaultFiles{WhatsAppFiles: s.o.Files, stage: stage, failure: fmt.Sprint(row["fail"])}
			from := stage
			if row["outside"] == true {
				from = external
			}
			if row["readonly"] == true {
				if e = os.Chmod(root, 0500); e != nil {
					t.Fatal(e)
				}
				defer os.Chmod(root, 0700)
			}
			e = s.promote(from, target)
			var failure any
			if api, ok := e.(*Error); ok {
				failure = map[string]any{"status": float64(api.Status), "message": api.Message}
			} else if row["readonly"] == true && os.IsPermission(e) {
				failure = map[string]any{"errno": "EACCES"}
			} else if e != nil {
				t.Fatal(e)
			}
			read := func(dir, name string) any {
				data, e := os.ReadFile(filepath.Join(dir, name))
				if e != nil {
					return nil
				}
				return string(data)
			}
			_, stageErr := os.Stat(stage)
			info, targetErr := os.Lstat(target)
			actual := map[string]any{"error": failure, "new": read(target, "new"), "old": read(target, "old"), "protected": read(external, "protected"), "stagingExists": stageErr == nil, "targetSymlink": targetErr == nil && info.Mode()&os.ModeSymlink != 0}
			if !reflect.DeepEqual(actual, source.Rows[index]) {
				t.Errorf("Source physical %s Go%v Source%v", fmt.Sprint(row["id"]), actual, source.Rows[index])
			}
		})
	}
}

type waFaultFiles struct {
	WhatsAppFiles
	stage, failure string
}

func (f waFaultFiles) Rename(from, to string) error {
	if f.failure == "rename" && from == f.stage {
		return fmt.Errorf("fixture rename failure")
	}
	return f.WhatsAppFiles.Rename(from, to)
}
func (f waFaultFiles) RemoveAll(path string) error {
	if f.failure == "remove-backup" && strings.Contains(path, ".backup-") {
		return fmt.Errorf("fixture backup delete failure")
	}
	return f.WhatsAppFiles.RemoveAll(path)
}
