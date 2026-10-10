package builtintools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceFileReadAnalyticsTextNotebookDedupAndMessageID(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type profile struct {
		Path   string         `json:"path"`
		Base64 string         `json:"base64"`
		Reads  []protocolRead `json:"reads"`
	}
	cases := []profile{}
	for i, row := range []struct {
		name, content string
		reads         []protocolRead
	}{
		{"plain.TXT", "first\r\n世界🦊\r\n", []protocolRead{{}, {Offset: 2, Limit: 1}}},
		{"bom.txt", "\ufefffoo\nbar", []protocolRead{{}, {}}},
		{"empty", "", []protocolRead{{}}},
		{".claude/projects/sid.jsonl", "{}\n", []protocolRead{{}}},
		{".claude/session-memory/notes.md", "private\n", []protocolRead{{}}},
		{"long.12345678901", "x\n", []protocolRead{{}, {}}},
		{"actual.ipynb", nativeNotebookFile, []protocolRead{{}, {}}},
	} {
		_ = i
		cases = append(cases, profile{row.name, base64.StdEncoding.EncodeToString([]byte(row.content)), row.reads})
	}
	root, _ := filepath.Abs("../../..")
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	config := filepath.Join(home, ".claude")
	tmp := filepath.Join(home, "tmp")
	for _, p := range []string{home, config, tmp} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	cmd := exec.Command(bun, "--no-env-file", filepath.Join(root, "scripts/quality-gate/go-parity/native-file-read-analytics.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "NODE_ENV=test", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"cases": cases})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var expected []struct {
		Events []analytics.Event `json:"events"`
	}
	if e = json.Unmarshal(raw, &expected); e != nil {
		t.Fatal(e, string(raw))
	}
	for i, row := range cases {
		s, owner, home, _ := fixture(t)
		s.options.Environment["CLAUDE_CONFIG_DIR"] = filepath.Join(home, ".claude")
		events := []analytics.Event{}
		s.options.ReportEvent = func(scope *cooperative.Scope, event analytics.Event) error {
			if !scope.BelongsTo(owner) {
				t.Fatal("foreign read event")
			}
			if event.Name == "tengu_file_operation" {
				event.Metadata.Set("filePathHash", quote("$PATH_HASH"))
			}
			events = append(events, event)
			return nil
		}
		path := filepath.Join(home, row.Path)
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		content, _ := base64.StdEncoding.DecodeString(row.Base64)
		if e = os.WriteFile(path, content, 0600); e != nil {
			t.Fatal(e)
		}
		for _, read := range row.Reads {
			in := input(`{}`)
			in.Set("file_path", quote(path))
			if read.Offset != 0 {
				in.Set("offset", rawValue(read.Offset))
			}
			if read.Limit != 0 {
				in.Set("limit", rawValue(read.Limit))
			}
			e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				assistant := streamhistory.Envelope{Record: input(`{"message":{"id":"private-assistant-message"}}`)}
				ctx := readAnalyticsContext(context.Background(), queryloop.JSONToolCallContext{Invocation: queryloop.Invocation{Assistant: &assistant}})
				_, e := s.read(ctx, in, scope)
				return e
			})
			if e != nil {
				t.Fatal(i, e)
			}
		}
		a, _ := json.Marshal(events)
		b, _ := json.Marshal(expected[i].Events)
		var got, want any
		_ = json.Unmarshal(a, &got)
		_ = json.Unmarshal(b, &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("case%d Go%s Source%s", i, a, b)
		}
	}
}

type protocolRead struct {
	Offset int `json:"offset,omitempty"`
	Limit  int `json:"limit,omitempty"`
}

func rawValue(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
