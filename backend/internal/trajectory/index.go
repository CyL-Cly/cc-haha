package trajectory

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type fileIndex struct {
	Stat             sessionstore.Snapshot
	Scanned          int64
	Prefix, Boundary string
	PrefixLen        int64
}

func (v *fileIndex) valid(ctx context.Context, f sessionstore.File, stat sessionstore.Snapshot) (bool, error) {
	if v.Stat.Dev != stat.Dev || v.Stat.Ino != stat.Ino || stat.Size < v.Scanned || stat.Size == v.Stat.Size && stat.MtimeNS != v.Stat.MtimeNS {
		return false, nil
	}
	p, e := hash(ctx, f, 0, v.PrefixLen)
	if e != nil {
		return false, e
	}
	b, e := hash(ctx, f, max(0, v.Scanned-4096), v.Scanned)
	return p == v.Prefix && b == v.Boundary, e
}
func (v *fileIndex) finish(ctx context.Context, f sessionstore.File, stat sessionstore.Snapshot, next int64) error {
	v.Stat = stat
	v.Scanned = next
	v.PrefixLen = min(4096, next)
	p, e := hash(ctx, f, 0, v.PrefixLen)
	if e != nil {
		return e
	}
	b, e := hash(ctx, f, max(0, next-4096), next)
	if e != nil {
		return e
	}
	v.Prefix, v.Boundary = p, b
	return nil
}

type turnCheckpoint struct {
	Offset int64
	Count  int
}
type turnIndex struct {
	fileIndex
	Total       int
	Checkpoints []turnCheckpoint
	TooLarge    bool
}

func (s *Service) trimCaches(key string) {
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.order = append(s.order, key)
	for len(s.order) > 8 {
		key := s.order[0]
		s.order = s.order[1:]
		delete(s.turns, key)
		delete(s.snapshot, key)
	}
}
func (s *Service) countBefore(ctx context.Context, f sessionstore.File, path string, end int64, main bool) (*int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if s.turns == nil {
		s.turns = map[string]*turnIndex{}
	}
	key := fmt.Sprintf("%t:%s", main, path)
	stat, e := f.Stat()
	if e != nil {
		return nil, e
	}
	index := s.turns[key]
	if index != nil {
		valid, e := index.valid(ctx, f, stat)
		if e != nil {
			return nil, e
		}
		if !valid {
			index = nil
			delete(s.turns, key)
		}
	}
	if index != nil {
		copied := *index
		copied.Checkpoints = append([]turnCheckpoint{}, index.Checkpoints...)
		index = &copied
	}
	if index == nil {
		index = &turnIndex{Checkpoints: []turnCheckpoint{{0, 0}}}
	}
	if stat.Size-index.Scanned > turnScanMaxBytes {
		index.TooLarge = true
		s.turns[key] = index
		s.trimCaches(key)
		return nil, nil
	}
	if index.TooLarge {
		return nil, nil
	}
	last := index.Checkpoints[len(index.Checkpoints)-1]
	next, e := Scan(ctx, f, index.Scanned, stat.Size, sessionstore.HistorySemanticRecordBytes, func(start, end int64, b, head []byte) {
		v := parse(b)
		if v != nil && !skipped(v, main) && realPrompt(v) {
			index.Total++
			if index.Total-last.Count >= 256 || start-last.Offset >= 4*1024*1024 {
				last = turnCheckpoint{start, index.Total - 1}
				index.Checkpoints = append(index.Checkpoints, last)
			}
		}
	})
	if e != nil {
		return nil, e
	}
	if e = index.finish(ctx, f, stat, next); e != nil {
		return nil, e
	}
	s.turns[key] = index
	s.trimCaches(key)
	if end >= index.Scanned {
		n := index.Total
		return &n, nil
	}
	checkpoint := index.Checkpoints[0]
	for _, p := range index.Checkpoints {
		if p.Offset > end {
			break
		}
		checkpoint = p
	}
	count := checkpoint.Count
	_, e = Scan(ctx, f, checkpoint.Offset, end, sessionstore.HistorySemanticRecordBytes, func(_, _ int64, b, head []byte) {
		v := parse(b)
		if v != nil && !skipped(v, main) && realPrompt(v) {
			count++
		}
	})
	return &count, e
}

type blobLocator struct {
	Kind       string
	Start, End int64
}
type scopeIndex struct {
	Markers         int
	Sys, Tools, Ctx string
	Rows            []Row
}
type snapshotIndex struct {
	fileIndex
	Blobs  map[string]blobLocator
	Scopes map[string]*scopeIndex
}

func conversationSource(v any) bool {
	if v == nil {
		return true
	}
	qs, ok := v.(string)
	return ok && (qs == "sdk" || qs == "repl_main_thread" || strings.HasPrefix(qs, "repl_main_thread:") || strings.HasPrefix(qs, "agent:"))
}
func snapshotMarker(index *snapshotIndex, start, end int64, v Object) {
	if v["t"] == "blob" {
		h, k := text(v["h"]), text(v["k"])
		if hash16.MatchString(h) && (k == "sys" || k == "tools" || k == "ctx") {
			if _, ok := index.Blobs[h]; !ok {
				index.Blobs[h] = blobLocator{k, start, end}
			}
		}
		return
	}
	if v["t"] != "req" && v["t"] != "ctx" {
		return
	}
	scope := strings.TrimPrefix(first(v["scope"], "main"), "agent-")
	state := index.Scopes[scope]
	if state == nil {
		if len(index.Scopes) >= 128 {
			return
		}
		state = &scopeIndex{Rows: []Row{}}
		index.Scopes[scope] = state
	}
	ordinal := state.Markers
	state.Markers++
	r := Row{"id": fmt.Sprintf("s:%s:%d", scope, ordinal), "turn": nil, "ts": first(v["ts"]), "loc": [][2]int64{}}
	ref := Object{"scope": scope}
	if v["t"] == "req" {
		if !conversationSource(v["qs"]) {
			return
		}
		sys, tools := text(v["sys"]), text(v["tools"])
		if sys == "" && tools == "" {
			return
		}
		change := ""
		if state.Sys == "" && state.Tools == "" {
			change = "initial"
		} else {
			sc, tc := sys != state.Sys, tools != state.Tools
			if sc && tc {
				change = "system+tools"
			} else if sc {
				change = "system"
			} else if tc {
				change = "tools"
			}
		}
		if change == "" {
			return
		}
		r["kind"], r["label"] = "system", change
		name := "system prompt"
		if change == "tools" {
			name = "tools"
		}
		counts := []string{}
		if v["sysChars"] != nil {
			counts = append(counts, fmt.Sprintf("%s chars", commaNumber(int(number(v["sysChars"])))))
		}
		if v["toolCount"] != nil {
			counts = append(counts, fmt.Sprintf("%v tools", v["toolCount"]))
		}
		if len(counts) > 0 {
			name += " (" + strings.Join(counts, ", ") + ")"
		}
		r["preview"] = name
		ref["change"] = change
		for k, v := range map[string]string{"sys": sys, "tools": tools, "querySource": text(v["qs"]), "model": text(v["model"])} {
			if v != "" {
				ref[k] = v
			}
		}
		if change != "initial" {
			if state.Sys != "" {
				ref["prevSys"] = state.Sys
			}
			if state.Tools != "" {
				ref["prevTools"] = state.Tools
			}
		}
		if v["toolCount"] != nil {
			ref["toolCount"] = v["toolCount"]
		}
		if v["sysChars"] != nil {
			ref["sysChars"] = v["sysChars"]
		}
		state.Sys, state.Tools = sys, tools
	} else {
		ctx := text(v["ctx"])
		if ctx == "" || ctx == state.Ctx {
			return
		}
		r["kind"], r["label"], r["preview"] = "context", "user_context", "user context"
		ref["change"], ref["ctx"] = "context", ctx
		if state.Ctx != "" {
			ref["prevCtx"] = state.Ctx
		}
		state.Ctx = ctx
	}
	r["snapshot"] = ref
	state.Rows = append(state.Rows, r)
	if len(state.Rows) > 2000 {
		state.Rows = state.Rows[len(state.Rows)-2000:]
	}
}
func commaNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func (s *Service) snapshotIndex(ctx context.Context, path string) (*snapshotIndex, error) {
	f, e := (sessionstore.OSFileSystem{}).Open(path)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if s.snapshot == nil {
		s.snapshot = map[string]*snapshotIndex{}
	}
	index := s.snapshot[path]
	if index != nil {
		valid, e := index.valid(ctx, f, stat)
		if e != nil {
			return nil, e
		}
		if !valid {
			index = nil
			delete(s.snapshot, path)
		}
	}
	if index != nil {
		copied := *index
		copied.Blobs = map[string]blobLocator{}
		for k, v := range index.Blobs {
			copied.Blobs[k] = v
		}
		copied.Scopes = map[string]*scopeIndex{}
		for k, v := range index.Scopes {
			state := *v
			state.Rows = append([]Row{}, v.Rows...)
			copied.Scopes[k] = &state
		}
		index = &copied
	}
	if index == nil {
		index = &snapshotIndex{Blobs: map[string]blobLocator{}, Scopes: map[string]*scopeIndex{}}
	}
	if stat.Size-index.Scanned > turnScanMaxBytes {
		return nil, api(413, "HISTORY_SNAPSHOT_LIMIT", "Prompt snapshots exceed the viewing budget")
	}
	next, e := Scan(ctx, f, index.Scanned, stat.Size, sessionstore.HistorySemanticRecordBytes, func(start, end int64, b, head []byte) {
		if v := parse(b); v != nil {
			snapshotMarker(index, start, end, v)
		}
	})
	if e != nil {
		return nil, e
	}
	after, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if after.Dev != stat.Dev || after.Ino != stat.Ino || after.Size < stat.Size || after.Size == stat.Size && after.MtimeNS != stat.MtimeNS {
		return nil, changed("Prompt snapshot file changed; retry")
	}
	if e = index.finish(ctx, f, stat, next); e != nil {
		return nil, e
	}
	s.snapshot[path] = index
	s.trimCaches(path)
	return index, nil
}
func (s *Service) snapshots(ctx context.Context, path, scope string) ([]Row, map[string]blobLocator, error) {
	rows := []Row{}
	var blobs map[string]blobLocator
	err := s.reader().Budget().With(ctx, sessionstore.MetadataLane, func() error {
		index, e := s.snapshotIndex(ctx, path)
		if e != nil || index == nil {
			return e
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if state := index.Scopes[scope]; state != nil {
			rows = append(rows, state.Rows...)
		}
		blobs = map[string]blobLocator{}
		for k, v := range index.Blobs {
			blobs[k] = v
		}
		return nil
	})
	return rows, blobs, err
}
func (s *Service) Snapshot(ctx context.Context, path, hash string) (Object, error) {
	if !hash16.MatchString(hash) {
		return nil, api(400, "BAD_REQUEST", "Invalid snapshot hash")
	}
	var out Object
	err := s.reader().Budget().With(ctx, sessionstore.MetadataLane, func() error {
		index, e := s.snapshotIndex(ctx, path)
		if e != nil {
			return e
		}
		if index == nil {
			return api(404, "NOT_FOUND", "Snapshot not found: "+hash)
		}
		s.mu.Lock()
		loc, ok := index.Blobs[hash]
		s.mu.Unlock()
		if !ok {
			return api(404, "NOT_FOUND", "Snapshot not found: "+hash)
		}
		f, e := (sessionstore.OSFileSystem{}).Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		b, e := exact(ctx, f, loc.Start, loc.End-loc.Start)
		if e != nil {
			return e
		}
		v := parse(b)
		if v == nil || v["h"] != hash {
			return changed("Prompt snapshot file changed; retry")
		}
		out = Object{"hash": hash, "kind": loc.Kind}
		for _, k := range []string{"text", "json", "truncated"} {
			if v[k] != nil {
				out[k] = v[k]
			}
		}
		return nil
	})
	return out, err
}
