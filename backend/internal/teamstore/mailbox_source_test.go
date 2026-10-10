package teamstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func TestTeamMailboxActualSourceReadWriteMarkClearAndConcurrentWriters(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	for _, dir := range []string{".claude", "tmp"} {
		_ = os.MkdirAll(filepath.Join(home, dir), 0700)
	}
	message := func(id, from string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: raw(from)}, protocol.JSONField{Name: "text", Value: raw("Exact <text> 中文😀")}, protocol.JSONField{Name: "timestamp", Value: raw("2026-10-07T00:00:00.000Z")}, protocol.JSONField{Name: "color", Value: raw("blue")}, protocol.JSONField{Name: "summary", Value: raw("original & summary")}, protocol.JSONField{Name: "id", Value: raw(id)}, protocol.JSONField{Name: "future", Value: []byte(`{"keep":true}`)})
	}
	type scenario struct {
		Name, Team string
		Initial    *string
		Operations []protocol.JSONObject
	}
	op := func(kind string, fields ...protocol.JSONField) protocol.JSONObject {
		return protocol.NewJSONObject(append([]protocol.JSONField{{Name: "type", Value: raw(kind)}}, fields...)...)
	}
	cases := []scenario{}
	for i, kind := range []string{"missing", "writes", "index", "all", "predicate", "clear", "corrupt", "object", "null", "parallel"} {
		c := scenario{Name: kind + "😀", Team: "team/<" + kind, Operations: []protocol.JSONObject{}}
		if kind == "missing" {
			c.Team = ""
			c.Operations = append(c.Operations, op("clear"), op("all"), op("index", protocol.JSONField{Name: "index", Value: raw(0)}))
		} else if kind == "corrupt" || kind == "object" || kind == "null" {
			text := map[string]string{"corrupt": "invalid-json", "object": `{"unknown":"preserved"}`, "null": "null"}[kind]
			c.Initial = &text
			if kind != "object" {
				c.Operations = append(c.Operations, op("write", protocol.JSONField{Name: "message", Value: raw(message("mailbox-a", "first"))}))
			}
		} else if kind == "parallel" {
			messages := []protocol.JSONObject{}
			for i := 0; i < 6; i++ {
				messages = append(messages, message("mailbox-"+string(rune('a'+i)), "sender"))
			}
			c.Operations = append(c.Operations, op("parallel", protocol.JSONField{Name: "messages", Value: raw(messages)}))
		} else {
			for _, id := range []string{"mailbox-a", "mailbox-b"} {
				c.Operations = append(c.Operations, op("write", protocol.JSONField{Name: "message", Value: raw(message(id, id))}))
			}
			switch kind {
			case "index":
				c.Operations = append(c.Operations, op("index", protocol.JSONField{Name: "index", Value: raw(1)}), op("index", protocol.JSONField{Name: "index", Value: raw(-1)}))
			case "all":
				c.Operations = append(c.Operations, op("all"), op("all"))
			case "predicate":
				c.Operations = append(c.Operations, op("predicate", protocol.JSONField{Name: "from", Value: raw("mailbox-a")}))
			case "clear":
				c.Operations = append(c.Operations, op("clear"))
			}
		}
		_ = i
		cases = append(cases, c)
	}
	input := []map[string]any{}
	for _, c := range cases {
		row := map[string]any{"name": c.Name, "team": c.Team, "operations": c.Operations}
		if c.Initial != nil {
			row["initial"] = *c.Initial
		}
		input = append(input, row)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-mailbox.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_MAILBOX_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": input}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	nativeHome := t.TempDir()
	tasks, e := taskstore.New(taskstore.Options{ConfigDir: filepath.Join(nativeHome, ".claude"), PID: os.Getpid(), Now: time.Now})
	if e != nil {
		t.Fatal(e)
	}
	mailbox, e := NewMailbox(MailboxOptions{ConfigDir: filepath.Join(nativeHome, ".claude"), TaskStore: tasks, TeamName: func() string { return "inherited" }})
	if e != nil {
		t.Fatal(e)
	}
	for i, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			path := mailbox.Path(c.Name, c.Team)
			if c.Initial != nil {
				_ = os.MkdirAll(filepath.Dir(path), 0700)
				_ = os.WriteFile(path, []byte(*c.Initial), 0600)
			}
			for _, o := range c.Operations {
				switch o.String("type") {
				case "write":
					if e = mailbox.Write(c.Name, c.Team, object(o.Get("message"))); e != nil {
						t.Fatal(e)
					}
				case "parallel":
					var values []protocol.JSONObject
					_ = json.Unmarshal(o.Get("messages"), &values)
					var wg sync.WaitGroup
					for _, v := range values {
						wg.Add(1)
						go func(v protocol.JSONObject) {
							defer wg.Done()
							if e := mailbox.Write(c.Name, c.Team, v); e != nil {
								t.Error(e)
							}
						}(v)
					}
					wg.Wait()
				case "index":
					var index int
					_ = json.Unmarshal(o.Get("index"), &index)
					_ = mailbox.MarkIndex(c.Name, c.Team, index)
				case "all":
					_ = mailbox.MarkAll(c.Name, c.Team)
				case "predicate":
					_ = mailbox.MarkMatching(c.Name, c.Team, func(v protocol.JSONObject) bool { return v.String("from") == o.String("from") })
				case "clear":
					_ = mailbox.Clear(c.Name, c.Team)
				}
			}
			value := mailbox.Read(c.Name, c.Team)
			unread, err := mailbox.Unread(c.Name, c.Team)
			var message any
			if err != nil {
				message = err.Error()
			}
			var physical any
			if b, e := os.ReadFile(path); e == nil {
				physical = string(b)
			}
			var formatted any
			var values []protocol.JSONObject
			if json.Unmarshal(value, &values) == nil {
				formatted = FormatMessages(values)
			}
			actual := raw(map[string]any{"path": strings.TrimPrefix(path, nativeHome), "value": value, "unread": unread, "error": message, "physical": physical, "formatted": formatted})
			expected := source.Rows[i]
			if c.Name == "parallel😀" {
				normalize := func(data json.RawMessage) json.RawMessage {
					v := object(data)
					for _, key := range []string{"value", "unread"} {
						var rows []protocol.JSONObject
						_ = json.Unmarshal(v.Get(key), &rows)
						sort.Slice(rows, func(i, j int) bool { return rows[i].String("id") < rows[j].String("id") })
						v.Set(key, raw(rows))
					}
					v.Delete("physical")
					v.Delete("formatted")
					return raw(v)
				}
				actual, expected = normalize(actual), normalize(expected)
			}
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(expected, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("%s\n%s", actual, expected)
			}
		})
	}
}
