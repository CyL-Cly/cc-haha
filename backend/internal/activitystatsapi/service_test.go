package activitystatsapi

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

const fixtureNow = "2026-10-06T12:34:56.789Z"

func fixtureSource(t *testing.T, cases []object, zone string) []object {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-activity-stats.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "TZ=" + zone, "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_ACTIVITY_FIXTURE=1"}
	inputRows := []any{}
	for _, row := range cases {
		inputRows = append(inputRows, row)
	}
	command.Stdin = bytes.NewReader(encode(object{"now": fixtureNow, "cases": inputRows}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Actual Source activity stats %v: %s", err, stderr.String())
	}
	value, err := decode(output)
	if err != nil {
		t.Fatalf("Source output %q %v: %s", output, err, stderr.String())
	}
	rows := value.([]any)
	out := []object{}
	for _, row := range rows {
		out = append(out, row.(object))
	}
	return out
}
func fixtureService(t *testing.T, zone string) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{HomeDir: home, ConfigDir: config, Now: func() time.Time { date, _ := time.Parse(time.RFC3339Nano, fixtureNow); return date }, Location: location})
	if err != nil {
		t.Fatal(err)
	}
	return service, config
}
func seedCase(t *testing.T, config string, row object) {
	t.Helper()
	if !truthy(get(row, "continue")) {
		if err := os.RemoveAll(config); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(config, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if cache, exists := row["cache"]; exists {
		raw := encode(cache)
		if text, ok := cache.(string); ok {
			raw = []byte(text)
		}
		if err := os.WriteFile(filepath.Join(config, "stats-cache.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := get(row, "files").([]any)
	for _, value := range files {
		file := value.(object)
		path := filepath.Join(config, "projects", get(file, "path").(string))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		var raw []byte
		if text, ok := get(file, "raw").(string); ok {
			raw = []byte(text)
		} else {
			for _, entry := range get(file, "entries").([]any) {
				raw = append(raw, encode(entry)...)
				raw = append(raw, '\n')
			}
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if prefix := numberValue(get(file, "prefixBytes")); prefix > 0 {
			if err := os.Truncate(path, int64(prefix)); err != nil {
				t.Fatal(err)
			}
			handle, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = handle.Write(append([]byte{'\n'}, raw...)); err != nil {
				t.Fatal(err)
			}
			if err = handle.Close(); err != nil {
				t.Fatal(err)
			}
		}
		mtime := fixtureNow
		if value, ok := get(file, "mtime").(string); ok {
			mtime = value
		}
		date, _ := time.Parse(time.RFC3339Nano, mtime)
		if err := os.Chtimes(path, date, date); err != nil {
			t.Fatal(err)
		}
	}
}
func currentCache(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(path, "stats-cache.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	value, err := decode(raw)
	if err != nil {
		return string(raw)
	}
	return value
}
func sameJSON(t *testing.T, want, got any) {
	t.Helper()
	a, _ := decode(encode(want))
	b, _ := decode(encode(got))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Actual Source differs\nSource: %s\nGo:     %s", encode(want), encode(got))
	}
}
func transcript(kind string, timestamp any) object {
	return object{"type": kind, "timestamp": timestamp}
}
func assistant(timestamp any, model any, usage object, content ...any) object {
	row := transcript("assistant", timestamp)
	row["message"] = object{"model": model, "usage": usage, "content": content}
	return row
}
func file(path string, entries ...any) object { return object{"path": path, "entries": entries} }
func filesRange(rangeName string, files ...object) object {
	rows := []any{}
	for _, row := range files {
		rows = append(rows, row)
	}
	return object{"range": rangeName, "files": rows}
}
func TestActivityStatsAgainstActualSource(t *testing.T) {
	tool := func(name any, input any) object { return object{"type": "tool_use", "name": name, "input": input} }
	usage := object{"input_tokens": 1000.0, "output_tokens": 40.0, "cache_read_input_tokens": 300.0, "cache_creation_input_tokens": 100.0, "server_tool_use": object{"web_search_requests": 2.0}}
	a := assistant("2026-10-06T10:00:00Z", "deepseek-flash", usage, tool(" Bash ", object{"command": "echo isolated"}), tool("Skill", object{"skill": " verify "}))
	repeated := assistant("2026-10-06T10:01:00Z", "claude-opus-5-5", usage, tool("Read", object{}))
	repeated["requestId"] = "req"
	repeated["sessionId"] = "session"
	repeated["version"] = "1.2.3"
	get(repeated, "message").(object)["id"] = "reply"
	forked := assistant("2026-10-06T10:02:00Z", "claude-opus-5-5", usage)
	forked["forkedFrom"] = object{"sessionId": "original", "messageUuid": "message"}
	side := assistant("2026-10-06T10:03:00Z", "claude-sonnet-4", usage, tool("Write", nil))
	side["isSidechain"] = true
	brokenFirst := transcript("user", "not-a-date")
	delete(brokenFirst, "timestamp")
	numeric := assistant(float64(1791288000000), "unknown", object{"input_tokens": float64(1)})
	inherited := filesRange("7d", file("project/a.jsonl", transcript("user", "2026-09-15T01:00:00Z"), a))
	baselineCache := emptyCache()
	baselineCache["lastComputedDate"] = "2026-10-05"
	baselineCache["totalSessions"] = float64(90)
	baselineCache["totalMessages"] = float64(180)
	baselineCache["firstSessionDate"] = "2026-01-01T00:00:00Z"
	baselineCache["dailyActivity"] = []any{object{"date": "2026-01-01", "messageCount": float64(180), "sessionCount": float64(90), "toolCallCount": float64(2)}}
	baselineCache["toolUsage"] = object{"Read": float64(2), "discard": float64(-1), "string": "2"}
	baselineCache["hourCounts"] = object{"9": float64(90)}
	cases := []object{
		filesRange("all"), filesRange("7d"),
		filesRange("all", file("project/a.jsonl", transcript("user", "2026-10-06T09:59:00Z"), a)),
		filesRange("7d", file("project/a.jsonl", transcript("user", "2026-10-06T09:59:00Z"), a, repeated, repeated, forked, side)),
		filesRange("all", file("project/a.jsonl", transcript("user", "2026-10-04T23:58:00Z"), assistant("2026-10-05T00:03:00Z", "claude-sonnet-4", usage), a)),
		inherited,
		filesRange("30d", file("project/a.jsonl", transcript("system", "2026-09-07T00:00:00Z"), transcript("attachment", "2026-10-01T00:00:00Z"), transcript("progress", "2026-10-06T10:00:00Z"), a)),
		filesRange("7d", file("project/a.jsonl", brokenFirst, a)),
		filesRange("all", file("project/a.jsonl", transcript("user", nil), assistant(nil, "claude-3-haiku", usage))),
		filesRange("7d", file("project/a.jsonl", numeric)),
		filesRange("7d", file("project/a.jsonl", transcript("user", "2026-10-06T09:00:00Z"), transcript("system", "invalid"), a)),
		filesRange("7d", file("project/a.jsonl", transcript("user", "2026-10-06T09:00:00Z"), transcript("system", "invalid"))),
		filesRange("all", file("project/a.jsonl", object{"type": "speculation-accept", "timeSavedMs": float64(50)}, transcript("user", "2026-10-05T00:00:00Z"), a)),
		filesRange("7d", file("project/a.jsonl", object{"type": "speculation-accept", "timeSavedMs": "10"}, a)),
		filesRange("7d", file("project/a.jsonl", nil, a)),
		filesRange("7d", file("project/a.jsonl", assistant("2026-10-06T10:00:00Z", "claude-sonnet-4", usage, nil))),
		filesRange("7d", file("project/a.jsonl", assistant("2026-10-06T10:00:00Z", float64(42), usage))),
		filesRange("7d", file("project/a.jsonl", assistant("2026-10-06T10:00:00Z", "<synthetic>", usage, tool("Bash", object{})))),
		filesRange("7d", file("project/a.jsonl", assistant("2026-10-06T10:00:00Z", "claude-opus-5-5", object{"input_tokens": "100", "output_tokens": "2", "cache_creation": object{"ephemeral_5m_input_tokens": "3", "ephemeral_1h_input_tokens": float64(4)}}))),
		filesRange("all", file("project/a.jsonl", transcript("user", "2026-10-01T00:00:00Z"), a), file("project/a/subagents/agent-sub.jsonl", side), file("project/a/subagents/workflows/run/agent-workflow.jsonl", side), file("project/a/subagents/workflows/run/deep/agent-ignored.jsonl", a)),
		filesRange("7d", file("project/a.jsonl", transcript("user", "2026-10-06T10:00:00Z"), transcript("user", "2026-10-06T09:00:00Z")), file("project/b.jsonl", transcript("user", "2026-10-06T08:00:00Z"))),
		{"range": "7d", "files": []any{object{"path": "project/a.jsonl", "raw": "bad\n" + string(encode(a)) + "\n{broken\n" + string(encode(repeated)) + "\n"}}},
		{"range": "7d", "files": []any{object{"path": "project/a.jsonl", "entries": []any{a}, "mtime": "2026-09-01T00:00:00Z"}}},
		{"range": "all", "files": []any{file("project/a.jsonl", a)}, "cache": baselineCache},
		{"range": "all", "files": []any{file("project/a.jsonl", a)}, "cache": object{"version": float64(6), "totalSessions": float64(900)}},
		{"range": "all", "files": []any{file("project/a.jsonl", a)}, "cache": "invalid"},
	}
	for _, model := range []string{"claude-opus-5-5", "claude-opus-5", "claude-opus-4-7", "claude-fable-5-1", "claude-sonnet-5-5", "claude-3-5-haiku", " Vendor/CLAUDE-SONNET-4-20260501 ", "claude-sonnet-4-thinking", "claude-opus-4-9", "deepseek-v4-flash"} {
		for _, speed := range []string{"", "fast"} {
			u := spread(usage)
			u["speed"] = speed
			cases = append(cases, filesRange("7d", file("project/a.jsonl", assistant("2026-10-06T10:00:00Z", model, u))))
		}
	}
	for _, invalid := range []object{object{"hourCounts": undefined}, object{"modelUsage": object{"bad": nil}}, object{"totalSpeculationTimeSavedMs": undefined}, object{"lastComputedDate": undefined}} {
		cache := spread(baselineCache)
		for key, value := range invalid {
			if _, missing := value.(undefinedValue); missing {
				delete(cache, key)
			} else {
				cache[key] = value
			}
		}
		cases = append(cases, object{"range": "all", "files": []any{file("project/a.jsonl", a)}, "cache": cache})
	}
	incremental := spread(baselineCache)
	incremental["lastComputedDate"] = "2026-10-03"
	cases = append(cases, object{"range": "all", "cache": incremental, "files": []any{file("project/a.jsonl", assistant("2026-10-04T00:00:00Z", "claude-sonnet-4", usage, tool("Read", nil)), a)}})
	duplicateCache := spread(baselineCache)
	duplicateCache["dailyActivity"] = []any{object{"date": "2026-01-01", "messageCount": float64(300), "sessionCount": float64(100), "toolCallCount": float64(10)}, object{"date": "2026-01-01", "messageCount": float64(100), "sessionCount": float64(80), "toolCallCount": float64(5)}}
	duplicateCache["dailyModelTokens"] = []any{object{"date": "2026-01-01", "tokensByModel": object{"deepseek-flash": float64(90)}, "extra": true}, object{"date": "2026-01-01", "tokensByModel": object{"deepseek-flash": float64(2)}}}
	cases = append(cases, object{"range": "all", "cache": duplicateCache, "files": []any{file("project/a.jsonl", a)}})
	for _, timestamp := range []any{"2026-10-06T00:30:00+08:00", "2026-10-06T00:30:00", "2026-10-06", "2026-10-06T24:00:00.000Z", "2026-02-30T00:00:00.000Z", true, []any{"2026-10-06"}, []any{}, object{}, float64(0), "6", "0001-01-01T00:00:00Z", "+010000-01-01T00:00:00Z"} {
		cases = append(cases, filesRange("all", file("project/a.jsonl", assistant(timestamp, "deepseek-flash", object{"input_tokens": float64(1)}))))
	}
	for _, identity := range []object{object{"version": "foreign"}, object{"version": "1.2.3-beta"}, object{"version": float64(100)}, object{"sessionId": ""}, object{"requestId": ""}, object{"requestId": "different"}, object{"forkedFrom": object{"sessionId": "original", "messageUuid": ""}}, object{"forkedFrom": true}} {
		row := spread(repeated)
		for name, value := range identity {
			row[name] = value
		}
		cases = append(cases, filesRange("7d", file("project/a.jsonl", repeated, row)))
	}
	for _, shot := range []string{"1", "2", "0"} {
		row := filesRange("all", file("project/a.jsonl", assistant("2026-10-04T00:00:00Z", "claude-sonnet-4", usage, tool("Bash", object{"command": "gh pr create --body '" + shot + "-shotted by claude'"})), a), file("project/a/subagents/agent-sub.jsonl", assistant("2026-10-06T10:00:00Z", "deepseek-flash", usage, tool("PowerShell", object{"command": "gh pr create --body '3-shotted by model'"}))))
		row["shots"] = true
		cases = append(cases, row)
	}
	for _, fields := range []object{object{"input_tokens": object{}}, object{"input_tokens": true, "output_tokens": false, "cache_read_input_tokens": nil}, object{"input_tokens": []any{float64(1)}}, object{"input_tokens": float64(-10), "output_tokens": float64(3)}, object{"cache_creation": object{"ephemeral_5m_input_tokens": float64(2), "ephemeral_1h_input_tokens": float64(3)}, "cache_creation_input_tokens": float64(99)}, object{"input_tokens": "1e309"}} {
		cases = append(cases, filesRange("7d", file("project/a.jsonl", assistant("2026-10-06T10:00:00Z", "claude-sonnet-4", fields))))
	}
	for _, date := range []any{float64(0), float64(1), nil, undefined, true, object{}, []any{}} {
		for _, live := range []bool{false, true} {
			cache := spread(baselineCache)
			cache["dailyActivity"] = []any{object{"date": date, "messageCount": float64(10), "sessionCount": float64(1), "toolCallCount": float64(0)}}
			entry := any(transcript("progress", "2026-10-06T10:00:00Z"))
			if live {
				entry = a
			}
			cases = append(cases, object{"range": "all", "cache": cache, "files": []any{file("project/a.jsonl", entry)}})
		}
	}
	for _, entry := range []any{nil, false, "string", float64(0), object{}} {
		cache := spread(baselineCache)
		cache["dailyActivity"] = []any{entry}
		cases = append(cases, object{"range": "all", "cache": cache, "files": []any{file("project/a.jsonl", transcript("progress", "2026-10-06T10:00:00Z"))}})
	}
	source := fixtureSource(t, cases, "UTC")
	service, config := fixtureService(t, "UTC")
	for i, row := range cases {
		t.Run(strconvI(i), func(t *testing.T) {
			seedCase(t, config, row)
			if date, ok := get(row, "now").(string); ok {
				now, _ := time.Parse(time.RFC3339Nano, date)
				service.options.Now = func() time.Time { return now }
			}
			service.options.ShotStatsEnabled = truthy(get(row, "shots"))
			stats, err := service.Aggregate(context.Background(), get(row, "range").(string), service.Now())
			expected := source[i]
			if _, hasError := expected["error"]; hasError {
				if err == nil {
					t.Fatalf("Source rejected %s but Go succeeded %s", encode(get(expected, "error")), encode(stats.Data))
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, get(expected, "value"), stats.Data)
			}
			sameJSON(t, get(expected, "cache"), currentCache(t, config))
		})
	}
}
func strconvI(index int) string { return strconv.Itoa(index) }

func TestActivityCacheIncrementAndConcurrentReadsAgainstSource(t *testing.T) {
	usage := object{"input_tokens": float64(5)}
	cases := []object{
		filesRange("all", file("p/a.jsonl", assistant("2026-10-04T10:00:00Z", "claude-sonnet-4", usage), assistant("2026-10-05T10:00:00Z", "deepseek-flash", usage), assistant("2026-10-06T10:00:00Z", "deepseek-flash", usage))),
		{"range": "all", "continue": true},
		{"range": "all", "continue": true, "now": "2026-10-07T12:34:56.789Z", "files": []any{file("p/b.jsonl", assistant("2026-10-07T10:00:00Z", "claude-sonnet-4", usage))}},
		{"range": "all", "continue": true, "now": "2026-10-07T12:34:56.789Z"},
	}
	source := fixtureSource(t, cases, "Asia/Shanghai")
	service, config := fixtureService(t, "Asia/Shanghai")
	for i, row := range cases {
		seedCase(t, config, row)
		if raw, ok := get(row, "now").(string); ok {
			now, _ := time.Parse(time.RFC3339Nano, raw)
			service.options.Now = func() time.Time { return now }
		}
		stats, err := service.Aggregate(context.Background(), "all", service.Now())
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, get(source[i], "value"), stats.Data)
		sameJSON(t, get(source[i], "cache"), currentCache(t, config))
	}
	const callers = 20
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			stats, err := service.Aggregate(context.Background(), "all", service.Now())
			if err != nil {
				t.Error(err)
				return
			}
			sameJSON(t, get(source[len(source)-1], "value"), stats.Data)
		}()
	}
	wg.Wait()
}

func TestActivityBoundedJSONLTailAgainstActualSource(t *testing.T) {
	large := file("private/bounded.jsonl", assistant("2026-10-06T10:00:00Z", "deepseek-flash", object{"input_tokens": float64(3)}))
	large["prefixBytes"] = float64(maxJSONLBytes + 4096)
	cases := []object{filesRange("7d", large)}
	source := fixtureSource(t, cases, "UTC")
	service, config := fixtureService(t, "UTC")
	seedCase(t, config, cases[0])
	stats, err := service.Aggregate(context.Background(), "7d", service.Now())
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, get(source[0], "value"), stats.Data)
	if get(stats.Data, "totalMessages") != float64(1) {
		t.Fatal("Partial retained head must be skipped while complete tail is counted")
	}
}

func TestActivityAllPriceTiersMatchActualSource(t *testing.T) {
	catalogs := fixtureSource(t, []object{object{"action": "pricingCatalog"}}, "UTC")
	expected := get(catalogs[0], "value")
	names := []any{}
	for _, tier := range tiers {
		names = append(names, tier.prefix)
	}
	fast := []any{}
	for _, tier := range fastTiers {
		fast = append(fast, tier.prefix)
	}
	sameJSON(t, expected, object{"MODEL_TIERS": names, "FAST_MODE_TIERS": fast})
	cases := []object{}
	for _, model := range names {
		for _, speed := range []string{"", "fast"} {
			cases = append(cases, filesRange("7d", file("project/tier.jsonl", assistant("2026-10-06T00:00:00Z", model, object{"input_tokens": float64(127), "output_tokens": float64(49), "cache_read_input_tokens": float64(833), "cache_creation": object{"ephemeral_5m_input_tokens": float64(77), "ephemeral_1h_input_tokens": float64(88)}, "server_tool_use": object{"web_search_requests": float64(2)}, "speed": speed}))))
		}
	}
	source := fixtureSource(t, cases, "UTC")
	service, config := fixtureService(t, "UTC")
	for i, row := range cases {
		seedCase(t, config, row)
		stats, err := service.Aggregate(context.Background(), "7d", service.Now())
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, get(source[i], "value"), stats.Data)
	}
}
