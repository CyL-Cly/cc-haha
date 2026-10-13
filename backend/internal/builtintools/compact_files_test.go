package builtintools

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompactRestoresFreshFilesBoundedCacheAndPreservedRead(t *testing.T) {
	service, owner, home, permissions := fixture(t)
	for i := range 7 {
		path := filepath.Join(home, fmt.Sprintf("%d.txt", i))
		if e := os.WriteFile(path, []byte("cached old text"), 0600); e != nil {
			t.Fatal(e)
		}
		call(t, service, owner, "Read", input(fmt.Sprintf(`{"file_path":%q}`, path)))
		if e := os.WriteFile(path, []byte("fresh content "+fmt.Sprint(i)), 0600); e != nil {
			t.Fatal(e)
		}
	}
	permissions.Set("alwaysDenyRules", raw(map[string][]string{"projectSettings": {"Read(6.txt)"}}))
	var restored []protocol.JSONObject
	factory, _ := messages.NewFactory(func() string { return "restored-id" }, func() time.Time { return time.UnixMilli(1791766923456) })
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		restored, e = service.RestoreCompactFiles(context.Background(), scope, nil, factory)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if len(restored) > 5 || len(restored) == 0 {
		t.Fatal("file restore not bounded", len(restored))
	}
	for _, record := range restored {
		body, _ := record.Object("attachment")
		if body.String("filename") == filepath.Join(home, "6.txt") {
			t.Fatal("restore bypassed explicit deny")
		}
		content, _ := body.Object("content")
		file, _ := content.Object("file")
		if !strings.HasPrefix(file.String("content"), "fresh content ") {
			t.Fatal("restore reused stale cache", file)
		}
	}
	// A kept dedup stub points at old content that compaction discarded. The
	// real file must be injected again despite the kept Read tool_use.
	stubPath := filepath.Join(home, "1.txt")
	call(t, service, owner, "Read", input(fmt.Sprintf(`{"file_path":%q}`, stubPath)))
	stubKeep := []protocol.JSONObject{input(fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"stub-read","name":"Read","input":{"file_path":%q}}]}}`, stubPath)), input(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"stub-read","content":"File unchanged since last read. The content from the earlier Read tool_result in this conversation is still current — refer to that instead of re-reading."}]}}`)}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		files, e := service.RestoreCompactFiles(context.Background(), scope, stubKeep, factory)
		if e != nil {
			return e
		}
		found := false
		for _, record := range files {
			body, _ := record.Object("attachment")
			found = found || body.String("filename") == stubPath
		}
		if !found {
			return fmt.Errorf("dedup stub suppressed fresh file restoration")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// A preserved full Read already carries that file. Its cache must be cleared,
	// while no duplicate attachment is added for the same kept tool result.
	path := filepath.Join(home, "0.txt")
	call(t, service, owner, "Read", input(fmt.Sprintf(`{"file_path":%q}`, path)))
	keep := []protocol.JSONObject{input(fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"kept-read","name":"Read","input":{"file_path":%q}}]}}`, path)), input(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"kept-read","content":"full preserved text"}]}}`)}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		files, e := service.RestoreCompactFiles(context.Background(), scope, keep, factory)
		if e != nil {
			return e
		}
		for _, record := range files {
			body, _ := record.Object("attachment")
			if body.String("filename") == path {
				return fmt.Errorf("duplicate file restore for preserved read")
			}
		}
		_, exists, e := service.options.Cache.Get(scope, path)
		if exists {
			return fmt.Errorf("cleared cache resurrected compacted full-read state")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
