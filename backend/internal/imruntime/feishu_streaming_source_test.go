package imruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualFeishuSourceMarkdownTablesImagesCodeBlocksAndUTF16(t *testing.T) {
	table := "|A|B|\n|-|-|\n|1|2|"
	texts := []string{"", "hello 中文 🧡", "# Head\n## Head2\n\ntext", "# Mixed\n### Head\n\n|A|B|\n|-|-|\n|1|2|\nnext", "**Bold**\n\n" + table + "\n\nparagraph", "```js\n# Keep\n|code|table|\n\n\n\n```\ntext", "image ![keep](img_key) ![omit](https://private.invalid/image) end", table + "\n\n" + table + "\n\n" + table + "\n\n" + table + "\n\n" + table, "中文 🧡\n" + table + "\n\n```\n" + table + "\n```"}
	rows := []map[string]any{}
	for _, text := range texts {
		for _, version := range []int{1, 2} {
			for _, limit := range []int{-1, 0, 3} {
				rows = append(rows, map[string]any{"text": text, "version": version, "limit": limit})
			}
		}
	}
	want := runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "markdown", "rows": rows}, t.TempDir())
	got := []any{}
	for _, row := range rows {
		text := NewText(row["text"].(string))
		tables := []any{}
		for _, table := range FeishuFindMarkdownTables(text) {
			tables = append(tables, map[string]any{"index": table.Index, "length": table.Length, "raw": table.Raw})
		}
		got = append(got, map[string]any{"optimized": FeishuOptimizeMarkdown(text, row["version"].(int)), "sanitized": FeishuSanitizeCardText(text, row["limit"].(int)), "tables": tables})
	}
	exactJSON(t, raw(got), want, "actual Source Feishu markdown rendering")
}
func TestActualFeishuSourcePermissionAndStreamingCardBuilders(t *testing.T) {
	home := t.TempDir()
	work := home + "/repo"
	rows := []map[string]any{}
	inputs := []any{map[string]any{"file_path": work + "/inside.txt", "command": "private command", "pattern": "needle", "path": work, "url": "https://fixture.invalid", "query": "search"}, map[string]any{"file_path": home + "/outside.txt", "notebook_path": work + "/notebook", "command": strings.Repeat("🧡", 180)}, map[string]any{"file_path": "", "notebook_path": work + "/fallback", "pattern": "", "path": work + "/..hidden"}, nil}
	for _, tool := range []string{"Write", "Edit", "MultiEdit", "NotebookEdit", "Read", "Bash", "BashOutput", "Grep", "Glob", "WebFetch", "WebSearch", "unknown"} {
		for _, input := range inputs {
			rows = append(rows, map[string]any{"kind": "permission", "tool": tool, "input": input, "request": "approve-fixture", "work": work})
		}
	}
	for _, text := range []string{"", "raw 中文 🧡"} {
		rows = append(rows, map[string]any{"kind": "rendered", "text": text}, map[string]any{"kind": "error", "text": text})
	}
	rows = append(rows, map[string]any{"kind": "initial"})
	want := runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "cards", "rows": rows}, home)
	got := []any{}
	for _, row := range rows {
		switch row["kind"] {
		case "permission":
			got = append(got, FeishuPermissionCard(NewText(row["tool"].(string)), raw(row["input"]), NewText("approve-fixture"), NewText(work), NewText(home)))
		case "initial":
			got = append(got, FeishuInitialStreamingCard())
		case "rendered":
			got = append(got, FeishuRenderedCard(NewText(row["text"].(string))))
		case "error":
			got = append(got, FeishuErrorCard(NewText(row["text"].(string))))
		}
	}
	exactJSON(t, raw(got), want, "actual original Feishu card builders")
}
func TestActualFeishuSourceStreamingLifecycleFallbackFailuresAndTerminalOnlyAnswer(t *testing.T) {
	for _, profile := range []string{"normal", "prebuffer", "create-fallback", "send-fallback", "rate-limit", "table-limit", "unknown-three", "final-settings-error", "abort-settings-error", "idle-final", "double-failure"} {
		t.Run(profile, func(t *testing.T) {
			actions := []map[string]any{{"method": "create"}, {"method": "reason", "text": "分析 private"}, {"method": "tool", "id": "tool-1", "name": "Read"}, {"method": "append", "text": "# Answer\nprivate答复 🧡"}, {"method": "tick", "ms": 100}, {"method": "done", "id": "tool-1", "name": "Read"}, {"method": "append", "text": "\nsecond"}, {"method": "tick", "ms": 100}, {"method": "append", "text": "\nthird"}, {"method": "tick", "ms": 100}, {"method": "flush"}, {"method": "final"}, {"method": "append", "text": "must-ignore"}}
			if profile == "prebuffer" {
				actions = append([]map[string]any{{"method": "append", "text": "beforecreate"}}, actions...)
			}
			if profile == "abort-settings-error" {
				actions[len(actions)-2] = map[string]any{"method": "abort", "text": "fixture failed"}
			}
			if profile == "idle-final" {
				actions = []map[string]any{{"method": "final"}, {"method": "append", "text": "must-ignore"}}
			}
			run := func(source bool) (json.RawMessage, json.RawMessage) {
				var mu sync.Mutex
				packets := []any{}
				messageCount, streamCount := 0, 0
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					p, _ := io.ReadAll(req.Body)
					mu.Lock()
					packets = append(packets, map[string]any{"path": req.URL.RequestURI(), "method": req.Method, "body": json.RawMessage(p)})
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					switch req.URL.Path {
					case "/open-apis/auth/v3/tenant_access_token/internal":
						_, _ = w.Write([]byte(`{"tenant_access_token":"private-token","expire":3600}`))
					case "/open-apis/cardkit/v1/cards":
						if profile == "create-fallback" || profile == "double-failure" {
							_, _ = w.Write([]byte(`{"code":7,"msg":"not enabled"}`))
						} else {
							_, _ = w.Write([]byte(`{"code":0,"data":{"card_id":"card-private"}}`))
						}
					case "/open-apis/im/v1/messages":
						messageCount++
						if profile == "double-failure" || profile == "send-fallback" && messageCount == 1 {
							w.WriteHeader(500)
							_, _ = w.Write([]byte(`{"code":9,"msg":"fixture message failure"}`))
						} else {
							_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"message-private"}}`))
						}
					case "/open-apis/cardkit/v1/cards/card-private/elements/streaming_content/content":
						streamCount++
						if profile == "rate-limit" && streamCount == 1 {
							_, _ = w.Write([]byte(`{"code":230020,"msg":"rate limited"}`))
						} else if profile == "table-limit" {
							_, _ = w.Write([]byte(`{"code":230099,"msg":"ErrCode: 11310; ErrMsg: card table number over limit"}`))
						} else if profile == "unknown-three" {
							_, _ = w.Write([]byte(`{"code":777,"msg":"fixture stream error"}`))
						} else {
							_, _ = w.Write([]byte(`{"code":0}`))
						}
					case "/open-apis/cardkit/v1/cards/card-private/settings":
						if profile == "final-settings-error" || profile == "abort-settings-error" {
							_, _ = w.Write([]byte(`{"code":888,"msg":"fixture settings failure"}`))
						} else {
							_, _ = w.Write([]byte(`{"code":0}`))
						}
					default:
						_, _ = w.Write([]byte(`{"code":0}`))
					}
				}))
				defer peer.Close()
				var result json.RawMessage
				if source {
					result = runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "streaming", "peer": peer.URL, "now": int64(1770000000000), "actions": actions}, t.TempDir())
				} else {
					clock := int64(1770000000000)
					type scheduled struct {
						fn    func()
						delay int64
						due   int64
					}
					timers := map[int]scheduled{}
					next := 0
					var timerMu sync.Mutex
					schedule := func(d time.Duration, fn func()) func() {
						timerMu.Lock()
						next++
						id := next
						timers[id] = scheduled{fn, d.Milliseconds(), clock + d.Milliseconds()}
						timerMu.Unlock()
						return func() { timerMu.Lock(); delete(timers, id); timerMu.Unlock() }
					}
					c := feishuSourceClient(t, peer, func() time.Time { return time.UnixMilli(clock) }, "feishu")
					card := NewFeishuStreamingCard(context.Background(), FeishuStreamingOptions{Client: c, ChatID: NewText("chat-fixture"), UUID: func() string { return "fixture-stream-uuid" }, Now: func() time.Time { return time.UnixMilli(clock) }, Schedule: schedule})
					rows := []any{}
					for _, a := range actions {
						var e error
						method := a["method"].(string)
						text, _ := a["text"].(string)
						id, _ := a["id"].(string)
						name, _ := a["name"].(string)
						switch method {
						case "create":
							e = card.EnsureCreated(context.Background())
						case "append":
							card.AppendText(NewText(text))
						case "reason":
							card.AppendReasoning(NewText(text))
						case "tool":
							card.StartTool(NewText(id), NewText(name))
						case "done":
							card.CompleteTool(NewText(id), NewText(name))
						case "flush":
							e = card.Flush(context.Background())
						case "tick":
							clock += int64(a["ms"].(int))
							timerMu.Lock()
							ids := []int{}
							for id := range timers {
								ids = append(ids, id)
							}
							sort.Ints(ids)
							timerMu.Unlock()
							for _, id := range ids {
								timerMu.Lock()
								timer, ok := timers[id]
								if ok && timer.due <= clock {
									delete(timers, id)
								} else {
									ok = false
								}
								timerMu.Unlock()
								if ok {
									timer.fn()
								}
							}
						case "final":
							e = card.Finalize(context.Background())
						case "abort":
							e = card.Abort(context.Background(), fmt.Errorf("%s", text))
						}
						if err := card.WaitForFlush(context.Background()); err != nil {
							t.Fatal(err)
						}
						errorText := ""
						if e != nil {
							row := feishuSourceError(e)
							errorText = row["error"].(string)
						}
						s := card.Snapshot()
						tools := []any{}
						for _, tool := range s.Tools {
							tools = append(tools, map[string]any{"id": tool.ID, "name": tool.Name, "status": tool.Status})
						}
						timerMu.Lock()
						ids := []int{}
						for id := range timers {
							ids = append(ids, id)
						}
						sort.Ints(ids)
						delays := []int64{}
						for _, id := range ids {
							delays = append(delays, timers[id].delay)
						}
						timerMu.Unlock()
						cardID, messageID := s.CardID, s.MessageID
						if len(cardID) == 0 {
							cardID = raw(nil)
						}
						if len(messageID) == 0 {
							messageID = raw(nil)
						}
						rows = append(rows, map[string]any{"error": errorText, "phase": s.Phase, "cardId": cardID, "messageId": messageID, "sequence": s.Sequence, "active": s.StreamActive, "failures": s.Failures, "text": s.Text, "reasoning": s.Reasoning, "tools": tools, "rendered": card.RenderedText(), "terminal": card.TerminalText(), "delays": delays})
					}
					card.Close()
					c.Close()
					result = raw(rows)
				}
				mu.Lock()
				receipt := raw(packets)
				mu.Unlock()
				return result, receipt
			}
			want, wantPackets := run(true)
			got, gotPackets := run(false)
			exactJSON(t, got, want, "actual original StreamingCard state/timers/rendering")
			exactJSON(t, gotPackets, wantPackets, "actual original StreamingCard physical HTTP sequence/fallback/UUID")
		})
	}
}
