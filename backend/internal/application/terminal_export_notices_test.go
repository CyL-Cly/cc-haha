package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestTerminalExportConsumesActualMemoryAuthCacheAndRenderEffects(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:9")
	o.Interactive = true
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["ANTHROPIC_AUTH_TOKEN"] = "owned-external-token"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	global := map[string]any{"primaryApiKey": "owned-legacy-key", "customApiKeyResponses": map[string]any{"approved": []string{o.Environment["ANTHROPIC_API_KEY"]}, "rejected": []string{}}, "future": map[string]any{"preserve": 17}}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.WorkDir, "CLAUDE.md"), []byte(strings.Repeat("😀", 20001)), 0600); err != nil {
		t.Fatal(err)
	}
	var secureReads atomic.Int32
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		secureReads.Add(1)
		return nil, os.ErrNotExist
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for i, name := range []string{"owned-export-first.txt", "owned-export-second.txt"} {
		if handled, err := s.InvokeTerminalCommand(context.Background(), "export", name, nil); !handled || err != nil {
			t.Fatal(handled, err)
		}
		output, err := os.ReadFile(filepath.Join(o.WorkDir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"Large CLAUDE.md", "40.0k chars > 40.0k", "Auth conflict: Using ANTHROPIC_API_KEY", "Auth conflict: Both a token (ANTHROPIC_AUTH_TOKEN)", "Trying to use ANTHROPIC_API_KEY"} {
			if !strings.Contains(string(output), text) {
				t.Fatal("actual export omitted native producer", text, string(output))
			}
		}
		if strings.Contains(string(output), "Claude account subscription token") || strings.Contains(string(output), "Large cumulative agent descriptions") {
			t.Fatal("original static caller's false gates became positive", string(output))
		}
		if i == 0 {
			global["reads"] = secureReads.Load()
		} else if global["reads"] != secureReads.Load() {
			t.Fatal("export re-read cached native credential", global["reads"], secureReads.Load())
		}
	}
	data, err := os.ReadFile(filepath.Join(config, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved protocol.JSONObject
	if err = saved.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	future, _ := saved.Object("future")
	if string(saved.Get("opus1mMergeNoticeSeenCount")) != "2" || saved.String("lastReleaseNotesSeen") != o.Version || string(future.Get("preserve")) != "17" {
		t.Fatal("real original render effects/unknown fields", string(data))
	}
}
