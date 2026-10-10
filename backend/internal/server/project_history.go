package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// This is Source ProjectSessionHistory's order snapshot, not another transcript
// reducer. The server's existing metadata/index projection owns every row.
type projectHistoryRow struct {
	info         sessionInfo
	root         string
	modifiedTime float64
}
type projectHistorySource struct {
	now                       func() int64
	scope, revision, mutation func() string
	load                      func(context.Context) ([]projectHistoryRow, error)
	hydrate                   func(context.Context, projectHistoryRow) (*sessionInfo, error)
}
type projectHistorySnapshot struct {
	scope, root string
	expiresAt   int64
	rows        []projectHistoryRow
}
type projectHistoryRequest struct {
	done chan struct{}
	rows []projectHistoryRow
	err  error
}
type projectSessionHistory struct {
	source         projectHistorySource
	mu             sync.Mutex
	catalogKey     string
	catalogExpires int64
	catalog        []projectHistoryRow
	requests       map[string]*projectHistoryRequest
	snapshots      map[string]*projectHistorySnapshot
	order          []string
	date           *activitystatsapi.IndexDateParser
}
type projectHistoryOptions struct {
	root                               string
	limit                              int
	hasLimit                           bool
	cursor, beforeModifiedAt, beforeID *string
}
type projectHistoryPage struct {
	Sessions   []sessionInfo `json:"sessions"`
	NextCursor *string       `json:"nextCursor"`
}

func newProjectSessionHistory(source projectHistorySource) *projectSessionHistory {
	return &projectSessionHistory{source: source, requests: map[string]*projectHistoryRequest{}, snapshots: map[string]*projectHistorySnapshot{}, date: activitystatsapi.NewIndexDateParser(time.Local)}
}
func projectHistoryExpired() error {
	return &apiError{409, "PROJECT_HISTORY_CURSOR_EXPIRED", "Project history cursor expired; retry from the first page"}
}
func projectHistoryChanged(message string) error {
	return &apiError{409, "PROJECT_HISTORY_CHANGED", message}
}
func (h *projectSessionHistory) time(value string) float64 {
	n, valid, e := h.date.Parse(value)
	if e != nil || !valid {
		return math.NaN()
	}
	return n
}
func (h *projectSessionHistory) loadCatalog(ctx context.Context) ([]projectHistoryRow, error) {
	for attempt := 0; attempt < 3; attempt++ {
		key, now := h.source.revision(), h.source.now()
		h.mu.Lock()
		if h.catalog != nil && h.catalogKey == key && h.catalogExpires > now {
			rows := h.catalog
			h.mu.Unlock()
			return rows, nil
		}
		pending := h.requests[key]
		owner := pending == nil
		if owner {
			pending = &projectHistoryRequest{done: make(chan struct{})}
			h.requests[key] = pending
		}
		h.mu.Unlock()
		if owner {
			// Source coalesces metadata scans independently of any one HTTP caller.
			pending.rows, pending.err = h.source.load(context.WithoutCancel(ctx))
			h.mu.Lock()
			if pending.err != nil && h.requests[key] == pending {
				delete(h.requests, key)
			}
			close(pending.done)
			h.mu.Unlock()
		} else {
			select {
			case <-pending.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if pending.err != nil {
			return nil, pending.err
		}
		if key != h.source.revision() {
			h.mu.Lock()
			if h.requests[key] == pending {
				delete(h.requests, key)
			}
			h.mu.Unlock()
			continue
		}
		rows := append([]projectHistoryRow{}, pending.rows...)
		times := map[string]float64{}
		for i := range rows {
			value := rows[i].info.ModifiedAt
			t, ok := times[value]
			if !ok {
				t = h.time(value)
				times[value] = t
			}
			rows[i].modifiedTime = t
		}
		compare := collate.New(language.English)
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i].info, rows[j].info
			byTime := rows[j].modifiedTime - rows[i].modifiedTime
			if byTime != 0 && !math.IsNaN(byTime) {
				return byTime < 0
			}
			if c := compare.CompareString(a.ID, b.ID); c != 0 {
				return c < 0
			}
			return compare.CompareString(a.Path, b.Path) < 0
		})
		h.mu.Lock()
		h.catalogKey = key
		h.catalog = rows
		h.catalogExpires = h.source.now() + 5000
		if h.requests[key] == pending {
			delete(h.requests, key)
		}
		h.mu.Unlock()
		return rows, nil
	}
	return nil, projectHistoryChanged("Project history changed during loading; retry from the first page")
}
func (h *projectSessionHistory) removeOrder(id string) {
	for i, key := range h.order {
		if key == id {
			h.order = append(h.order[:i], h.order[i+1:]...)
			return
		}
	}
}
func (h *projectSessionHistory) prune(scope string, now int64) {
	for id, snapshot := range h.snapshots {
		if snapshot.scope != scope || snapshot.expiresAt <= now {
			delete(h.snapshots, id)
			h.removeOrder(id)
		}
	}
	rows := 0
	for _, snapshot := range h.snapshots {
		rows += len(snapshot.rows)
	}
	for len(h.snapshots) > 16 || rows > 50000 && len(h.snapshots) > 1 {
		id := h.order[0]
		rows -= len(h.snapshots[id].rows)
		delete(h.snapshots, id)
		h.order = h.order[1:]
	}
}

var projectCursorCharacters = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var projectSnapshotID = regexp.MustCompile(`^[0-9a-f-]{36}$`)

func decodeProjectHistoryCursor(value string) (string, int, error) {
	invalid := badRequest("Invalid project history cursor")
	if len(value) > 256 || !projectCursorCharacters.MatchString(value) {
		return "", 0, invalid
	}
	raw, e := base64.RawURLEncoding.DecodeString(value)
	if e != nil {
		return "", 0, invalid
	}
	var fields struct {
		Snapshot string   `json:"snapshot"`
		Offset   *float64 `json:"offset"`
	}
	if json.Unmarshal(raw, &fields) != nil || !projectSnapshotID.MatchString(fields.Snapshot) || fields.Offset == nil {
		return "", 0, invalid
	}
	n := *fields.Offset
	if n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
		return "", 0, invalid
	}
	// Keep the check in floating point until comparison with the snapshot length.
	if n > float64(int(^uint(0)>>1)) {
		return fields.Snapshot, int(^uint(0) >> 1), nil
	}
	return fields.Snapshot, int(n), nil
}
func (h *projectSessionHistory) list(ctx context.Context, o projectHistoryOptions) (projectHistoryPage, error) {
	if trimJS(o.root) == "" || len(utf16.Encode([]rune(o.root))) > 8192 || strings.ContainsRune(o.root, 0) {
		return projectHistoryPage{}, badRequest("A valid projectRoot is required")
	}
	limit := o.limit
	if !o.hasLimit {
		limit = 50
	}
	if limit < 1 {
		return projectHistoryPage{}, badRequest("Invalid limit")
	}
	limit = min(100, limit)
	if (o.beforeModifiedAt == nil) != (o.beforeID == nil) || o.cursor != nil && o.beforeID != nil {
		return projectHistoryPage{}, badRequest("Use beforeModifiedAt and beforeId together on the first page only")
	}
	beforeTime := math.NaN()
	if o.beforeModifiedAt != nil {
		beforeTime = h.time(*o.beforeModifiedAt)
		if math.IsNaN(beforeTime) || *o.beforeID == "" || len(utf16.Encode([]rune(*o.beforeID))) > 256 || strings.ContainsRune(*o.beforeID, 0) {
			return projectHistoryPage{}, badRequest("Invalid history boundary")
		}
	}
	scope := h.source.scope()
	h.mu.Lock()
	h.prune(scope, h.source.now())
	h.mu.Unlock()
	var id string
	var snapshot *projectHistorySnapshot
	offset := 0
	if o.cursor != nil {
		var e error
		id, offset, e = decodeProjectHistoryCursor(*o.cursor)
		if e != nil {
			return projectHistoryPage{}, e
		}
		h.mu.Lock()
		snapshot = h.snapshots[id]
		h.mu.Unlock()
		if snapshot == nil {
			return projectHistoryPage{}, projectHistoryExpired()
		}
		if snapshot.scope != scope || snapshot.root != o.root {
			return projectHistoryPage{}, badRequest("Cursor does not belong to this project")
		}
		if offset > len(snapshot.rows) {
			return projectHistoryPage{}, badRequest("Invalid cursor position")
		}
	} else {
		catalog, e := h.loadCatalog(ctx)
		if e != nil {
			return projectHistoryPage{}, e
		}
		if scope != h.source.scope() {
			return projectHistoryPage{}, projectHistoryExpired()
		}
		seen := map[string]bool{}
		rows := []projectHistoryRow{}
		compare := collate.New(language.English)
		for _, row := range catalog {
			if row.root != o.root || seen[row.info.ID] {
				continue
			}
			seen[row.info.ID] = true
			if o.beforeModifiedAt != nil {
				t := row.modifiedTime
				if row.info.ID == *o.beforeID || !(t < beforeTime || t == beforeTime && compare.CompareString(row.info.ID, *o.beforeID) > 0) {
					continue
				}
			}
			rows = append(rows, row)
		}
		id, e = uuid()
		if e != nil {
			return projectHistoryPage{}, e
		}
		snapshot = &projectHistorySnapshot{scope: scope, root: o.root, expiresAt: h.source.now() + 300000, rows: rows}
		h.mu.Lock()
		h.snapshots[id] = snapshot
		h.order = append(h.order, id)
		h.prune(scope, h.source.now())
		h.mu.Unlock()
	}
	for attempt := 0; attempt < 3; attempt++ {
		mutation := h.source.mutation()
		sessions := []sessionInfo{}
		next := offset
		for next < len(snapshot.rows) && len(sessions) < limit && next-offset < limit*2 {
			row, e := h.source.hydrate(ctx, snapshot.rows[next])
			next++
			if e != nil {
				return projectHistoryPage{}, e
			}
			if row != nil {
				sessions = append(sessions, *row)
			}
		}
		if mutation != h.source.mutation() {
			continue
		}
		if scope != h.source.scope() {
			return projectHistoryPage{}, projectHistoryExpired()
		}
		h.mu.Lock()
		snapshot.expiresAt = h.source.now() + 300000
		h.snapshots[id] = snapshot
		h.removeOrder(id)
		h.order = append(h.order, id)
		h.prune(scope, h.source.now())
		h.mu.Unlock()
		page := projectHistoryPage{Sessions: sessions}
		if next < len(snapshot.rows) {
			raw, _ := json.Marshal(struct {
				Snapshot string `json:"snapshot"`
				Offset   int    `json:"offset"`
			}{id, next})
			value := base64.RawURLEncoding.EncodeToString(raw)
			page.NextCursor = &value
		}
		return page, nil
	}
	return projectHistoryPage{}, projectHistoryChanged("Project history changed during loading; retry from the first page")
}
func (s *Server) projectHistoryHTTP(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return methodNotAllowed(r.Method)
	}
	q := r.URL.Query()
	limit := 50
	if q.Has("limit") {
		value := q.Get("limit")
		if value == "" {
			return badRequest("Invalid limit parameter")
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return badRequest("Invalid limit parameter")
			}
		}
		n, e := strconv.ParseFloat(value, 64)
		if e != nil || n > 9007199254740991 || n < 1 {
			return badRequest("Invalid limit")
		}
		limit = int(min(100, n))
	}
	o := projectHistoryOptions{root: q.Get("projectRoot"), limit: limit, hasLimit: true}
	for key, target := range map[string]**string{"cursor": &o.cursor, "beforeModifiedAt": &o.beforeModifiedAt, "beforeId": &o.beforeID} {
		if q.Has(key) {
			v := q.Get(key)
			*target = &v
		}
	}
	h := s.projectHistoryManager()
	page, e := h.list(r.Context(), o)
	if e == nil {
		writeJSON(w, 200, page)
	}
	return e
}
