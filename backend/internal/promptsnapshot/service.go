// Package promptsnapshot appends the desktop-only request sidecar. It never
// waits on disk in the model request path and owns a bounded asynchronous queue.
package promptsnapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const MaxBlobChars = 1024 * 1024

type job struct {
	Path, Scope, Identity, Type string
	Marker                      map[string]any
	Blobs                       []blob
}
type blob struct {
	Key   string
	Value map[string]any
}
type Service struct {
	mu      sync.Mutex
	queue   chan job
	done    chan struct{}
	closed  bool
	pending sync.WaitGroup
	Now     func() time.Time
	OnError func(error)
}

func New(now func() time.Time, onError func(error)) *Service {
	if now == nil {
		now = time.Now
	}
	s := &Service{queue: make(chan job, 32), done: make(chan struct{}), Now: now, OnError: onError}
	go s.run()
	return s
}
func Enabled(env map[string]string) bool {
	if env["CLAUDE_CODE_ENTRYPOINT"] != "claude-desktop" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(env["CC_HAHA_PROMPT_SNAPSHOT"])) {
	case "0", "false", "off":
		return false
	}
	return true
}
func hash(b []byte) string         { h := sha256.Sum256(b); return hex.EncodeToString(h[:])[:16] }
func raw(v any) []byte             { b, _ := json.Marshal(v); return protocol.StringifyValidatedJavaScriptJSON(b) }
func str(v json.RawMessage) string { var s string; _ = json.Unmarshal(v, &s); return s }
func length(s string) int          { return len(utf16.Encode([]rune(s))) }
func boundString(s string, n int) (string, bool) {
	u := utf16.Encode([]rune(s))
	if len(u) <= n {
		return s, false
	}
	return string(utf16.Decode(u[:max(0, n)])), true
}
func (s *Service) enqueue(j job) {
	if !filepath.IsAbs(j.Path) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.pending.Add(1)
	select {
	case s.queue <- j:
	default:
		s.pending.Done()
		if s.OnError != nil {
			s.OnError(fmt.Errorf("prompt snapshot queue is full"))
		}
	}
}
func (s *Service) RecordRequest(path, scope, qs string, body json.RawMessage) {
	var request protocol.JSONObject
	if request.UnmarshalJSON(body) != nil {
		return
	}
	system := request.Get("system")
	sysText := ""
	if len(system) > 0 && system[0] == '"' {
		sysText = str(system)
	} else {
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(system, &blocks)
		parts := []string{}
		for _, block := range blocks {
			if block.Get("text") != nil {
				parts = append(parts, str(block.Get("text")))
			}
		}
		sysText = strings.Join(parts, "\n\n")
	}
	var tools []protocol.JSONObject
	_ = json.Unmarshal(request.Get("tools"), &tools)
	toolHashes := []string{}
	entries := []protocol.JSONObject{}
	for _, tool := range tools {
		name := str(tool.Get("name"))
		description := tool.Get("description")
		if description == nil {
			description = json.RawMessage("null")
		}
		schema := tool.Get("input_schema")
		if schema == nil {
			schema = json.RawMessage("null")
		}
		toolHashes = append(toolHashes, hash(raw([]json.RawMessage{raw(name), description, schema})))
		entry := protocol.NewJSONObject()
		entry.Set("name", raw(name))
		if tool.Get("description") != nil {
			entry.Set("description", tool.Get("description"))
		}
		if tool.Get("input_schema") != nil {
			entry.Set("input_schema", tool.Get("input_schema"))
		}
		entries = append(entries, entry)
	}
	sysHash, toolsHash := hash([]byte(sysText)), hash(raw(toolHashes))
	model := str(request.Get("model"))
	identity := string(raw([]any{sysHash, toolsHash, qs, model}))
	if scope == "" {
		scope = "main"
	}
	ts := s.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	sys, bounded := boundString(sysText, MaxBlobChars)
	sysBlob := map[string]any{"t": "blob", "k": "sys", "h": sysHash, "ts": ts, "text": sys}
	if bounded {
		sysBlob["truncated"] = true
	}
	boundedTools, truncated := boundTools(entries)
	toolsBlob := map[string]any{"t": "blob", "k": "tools", "h": toolsHash, "ts": ts, "json": boundedTools}
	if truncated {
		toolsBlob["truncated"] = true
	}
	marker := map[string]any{"t": "req", "ts": ts, "scope": scope, "sys": sysHash, "tools": toolsHash, "model": model, "toolCount": len(tools), "sysChars": length(sysText)}
	if qs != "" {
		marker["qs"] = qs
	}
	s.enqueue(job{path, scope, identity, "req", marker, []blob{{"sys:" + sysHash, sysBlob}, {"tools:" + toolsHash, toolsBlob}}})
}
func boundTools(entries []protocol.JSONObject) ([]protocol.JSONObject, bool) {
	size := length(string(raw(entries)))
	if size <= MaxBlobChars {
		return entries, false
	}
	out := make([]protocol.JSONObject, len(entries))
	for i, e := range entries {
		out[i] = e.Clone()
	}
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return len(out[order[a]].Get("input_schema")) > len(out[order[b]].Get("input_schema"))
	})
	for _, i := range order {
		if size <= MaxBlobChars {
			break
		}
		if schema := out[i].Get("input_schema"); schema != nil {
			out[i].Delete("input_schema")
			size = length(string(raw(out)))
		}
	}
	kept := []protocol.JSONObject{}
	size = 2
	for _, e := range out {
		cost := length(string(raw(e))) + 1
		if size+cost > MaxBlobChars {
			break
		}
		kept = append(kept, e)
		size += cost
	}
	return kept, true
}
func (s *Service) RecordContext(path, scope string, values map[string]string) {
	if scope == "" {
		scope = "main"
	}
	keys := []string{}
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := [][2]string{}
	context := protocol.NewJSONObject()
	total := 0
	for _, k := range keys {
		pairs = append(pairs, [2]string{k, values[k]})
		context.Set(k, raw(values[k]))
		total += length(k) + length(values[k])
	}
	h := hash(raw(pairs))
	truncated := total > MaxBlobChars
	if truncated {
		order := append([]string{}, keys...)
		sort.SliceStable(order, func(a, b int) bool { return length(values[order[a]]) > length(values[order[b]]) })
		excess := total - MaxBlobChars
		for _, k := range order {
			if excess <= 0 {
				break
			}
			n := length(values[k])
			v, _ := boundString(values[k], max(0, n-excess))
			context.Set(k, raw(v))
			excess -= n - length(v)
		}
	}
	ts := s.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	value := map[string]any{"t": "blob", "k": "ctx", "h": h, "ts": ts, "json": context}
	if truncated {
		value["truncated"] = true
	}
	s.enqueue(job{path, scope, h, "ctx", map[string]any{"t": "ctx", "ts": ts, "scope": scope, "ctx": h}, []blob{{"ctx:" + h, value}}})
}
func (s *Service) run() {
	defer close(s.done)
	persisted := map[string]map[string]bool{}
	last := map[string]string{}
	failures := map[string]int{}
	disabled := map[string]bool{}
	for j := range s.queue {
		func() {
			defer s.pending.Done()
			if disabled[j.Path] {
				return
			}
			key := j.Path + "\x00" + j.Scope + "\x00" + j.Type
			if last[key] == j.Identity {
				return
			}
			known := persisted[j.Path]
			if known == nil {
				known = map[string]bool{}
				persisted[j.Path] = known
			}
			payload := []byte{}
			pending := []string{}
			for _, b := range j.Blobs {
				if !known[b.Key] {
					payload = append(payload, raw(b.Value)...)
					payload = append(payload, '\n')
					pending = append(pending, b.Key)
				}
			}
			payload = append(payload, raw(j.Marker)...)
			payload = append(payload, '\n')
			err := os.MkdirAll(filepath.Dir(j.Path), 0700)
			if err == nil {
				var f *os.File
				f, err = os.OpenFile(j.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
				if err == nil {
					n, e := f.Write(payload)
					err = e
					if err == nil && n != len(payload) {
						err = fmt.Errorf("short prompt snapshot append")
					}
					closeErr := f.Close()
					if err == nil {
						err = closeErr
					}
				}
			}
			if err != nil {
				failures[j.Path]++
				delete(persisted, j.Path)
				if s.OnError != nil {
					s.OnError(err)
				}
				if failures[j.Path] >= 3 {
					disabled[j.Path] = true
				}
				return
			}
			failures[j.Path] = 0
			for _, k := range pending {
				known[k] = true
			}
			last[key] = j.Identity
		}()
	}
}
func (s *Service) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.pending.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-done:
		return nil
	}
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-s.done:
		return nil
	}
}
