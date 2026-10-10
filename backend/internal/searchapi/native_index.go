package searchapi

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeIndexAdapter ports the original session-service search consumers onto
// the one primary coordinator. Nil denotes unavailable/stale evidence, never
// an authoritative empty result from an unfinished index.
type NativeIndexAdapter struct {
	index     *localindex.Coordinator
	scope     string
	parseDate func(string) (float64, bool, error)
}

func NewNativeIndexAdapter(index *localindex.Coordinator, scope string, parseDate func(string) (float64, bool, error)) (*NativeIndexAdapter, error) {
	if index == nil || !filepath.IsAbs(scope) || parseDate == nil {
		return nil, errors.New("Search index adapter requires the shared coordinator, scope and actual date parser")
	}
	return &NativeIndexAdapter{index, scope, parseDate}, nil
}
func metadataRow(row protocol.JSONObject) Metadata {
	return Metadata{Title: str(row.Get("title")), ProjectPath: str(row.Get("projectPath")), ModifiedAt: str(row.Get("modifiedAt")), WorkDir: optionalString(row.Get("workDir"))}
}
func optionalString(v []byte) *string {
	if !isString(v) {
		return nil
	}
	return ptr(str(v))
}
func (a *NativeIndexAdapter) Metadata(ctx context.Context, paths []string) (map[string]Metadata, error) {
	if !a.index.ConsumerAvailable() {
		return nil, nil
	}
	epoch := a.index.Epoch()
	wanted := map[string]bool{}
	projects := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		path, e := filepath.Abs(path)
		if e != nil {
			return nil, e
		}
		wanted[path] = true
		project := filepath.Base(filepath.Dir(path))
		if !seen[project] {
			projects = append(projects, project)
			seen[project] = true
		}
	}
	out := map[string]Metadata{}
	limit := float64(2147483647)
	for _, project := range projects {
		page, e := a.index.List(ctx, localindex.SessionListOptions{Project: &project, Limit: &limit})
		if e != nil {
			return nil, nil
		}
		if page == nil {
			return nil, nil
		}
		for _, row := range page.Sessions {
			path, e := filepath.Abs(str(row.Get("transcriptPath")))
			if e != nil {
				return nil, nil
			}
			if wanted[path] {
				out[path] = metadataRow(row)
			}
		}
	}
	if epoch != a.index.Epoch() || !a.index.ConsumerAvailable() {
		return nil, nil
	}
	return out, nil
}
func (a *NativeIndexAdapter) ReadEntriesAtLines(ctx context.Context, path string, lines map[int64]bool) (*TargetedEntries, error) {
	if !a.index.ConsumerAvailable() {
		return nil, nil
	}
	epoch := a.index.Epoch()
	page, e := a.index.Locators(ctx, path, nil)
	if e != nil || page == nil {
		return nil, nil
	}
	byLine := map[int64]localindex.EntryLocator{}
	for _, entry := range page.Entries {
		byLine[entry.JSONLLine] = entry
	}
	selected := []localindex.EntryLocator{}
	for line := range lines {
		entry, ok := byLine[line]
		if !ok {
			return nil, nil
		}
		if entry.EntryType == "user" || entry.EntryType == "assistant" {
			selected = append(selected, entry)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].JSONLLine < selected[j].JSONLLine })
	page.Entries = selected
	result := localindex.ReadEntriesByLocator(ctx, path, filepath.Join(a.scope, "projects"), filepath.Base(filepath.Dir(path)), *page)
	if result == nil || epoch != a.index.Epoch() || !a.index.ConsumerAvailable() {
		return nil, nil
	}
	out := &TargetedEntries{Entries: []EntryLine{}, BytesRead: result.BytesRead, RangesRead: result.RangesRead}
	for n, entry := range result.Entries {
		out.Entries = append(out.Entries, EntryLine{entry, selected[n].JSONLLine})
	}
	return out, nil
}
func sameSnapshot(a, b localindex.FileSnapshot) bool {
	return a.Size == b.Size && a.MtimeMs == b.MtimeMs && a.CtimeMs == b.CtimeMs && sameOptionalString(a.FileIdentity, b.FileIdentity)
}
func nativeSnapshot(path string) (localindex.FileSnapshot, error) {
	return localindex.SnapshotPath(path)
}
func sameStatus(a, b localindex.Status) bool {
	return b.State == "ready" && b.DegradedSources == 0 && b.Discovered == b.Indexed && b.Indexed == a.Indexed && sameOptionalString(a.LastUpdatedAt, b.LastUpdatedAt)
}
func (a *NativeIndexAdapter) GetCandidatesForFilters(ctx context.Context, o localindex.ContentQueryOptions) ([]FilterCandidate, error) {
	if o.Project == nil || !directProject(raw(*o.Project)) || !a.index.ConsumerAvailable() {
		return nil, nil
	}
	epoch := a.index.Epoch()
	before := a.index.Status()
	if before.State != "ready" || before.DegradedSources != 0 || before.Discovered != before.Indexed {
		return nil, nil
	}
	if invalidFilterDate(o) {
		return []FilterCandidate{}, nil
	}
	all, e := a.index.FindCandidates(ctx, localindex.ContentQueryOptions{Project: o.Project})
	if e != nil || all == nil {
		return nil, nil
	}
	filtered := all
	if o.ModifiedAfterMs != nil || o.ModifiedBeforeMs != nil {
		filtered, e = a.index.FindCandidates(ctx, o)
		if e != nil || filtered == nil {
			return nil, nil
		}
	}
	projectsRoot := filepath.Join(a.scope, "projects")
	projectRoot := filepath.Join(projectsRoot, *o.Project)
	relative, e := filepath.Rel(projectsRoot, projectRoot)
	if e != nil || relative != *o.Project {
		return nil, nil
	}
	directoryBefore, e := nativeSnapshot(projectRoot)
	if e != nil {
		return nil, nil
	}
	info, e := os.Stat(projectRoot)
	if e != nil || !info.IsDir() {
		return nil, nil
	}
	entries, e := readDirectory(projectRoot)
	if e != nil {
		return nil, nil
	}
	canonical := map[string]bool{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
			canonical[filepath.Join(projectRoot, entry.Name())] = true
		}
	}
	validate := func(rows []protocol.JSONObject) ([]FilterCandidate, map[string]bool) {
		out := []FilterCandidate{}
		paths := map[string]bool{}
		for _, row := range rows {
			path := str(row.Get("transcriptPath"))
			project, id := str(row.Get("projectPath")), str(row.Get("id"))
			if project == "" || filepath.Base(project) != project || path != filepath.Join(projectsRoot, project, id+".jsonl") || project != *o.Project {
				return nil, nil
			}
			_, valid, e := a.parseDate(str(row.Get("modifiedAt")))
			if e != nil || !valid {
				return nil, nil
			}
			out = append(out, FilterCandidate{path, metadataRow(row)})
			paths[path] = true
		}
		return out, paths
	}
	allValues, allPaths := validate(all)
	result, resultPaths := validate(filtered)
	if allPaths == nil || resultPaths == nil || len(canonical) != len(allPaths) {
		return nil, nil
	}
	for path := range canonical {
		if !allPaths[path] {
			return nil, nil
		}
	}
	currentFiltered := map[string]bool{}
	for _, candidate := range allValues {
		info, e := os.Stat(candidate.Path)
		if e != nil || !info.Mode().IsRegular() {
			return nil, nil
		}
		snapshot, e := nativeSnapshot(candidate.Path)
		if e != nil {
			return nil, nil
		}
		if o.ModifiedAfterMs == nil || snapshot.MtimeMs >= *o.ModifiedAfterMs {
			if o.ModifiedBeforeMs == nil || snapshot.MtimeMs <= *o.ModifiedBeforeMs {
				currentFiltered[candidate.Path] = true
			}
		}
		for n := range result {
			if result[n].Path == candidate.Path {
				copy := snapshot
				result[n].Metadata.SourceSnapshot = &copy
			}
		}
	}
	if o.ModifiedAfterMs != nil || o.ModifiedBeforeMs != nil {
		if len(currentFiltered) != len(resultPaths) {
			return nil, nil
		}
		for path := range currentFiltered {
			if !resultPaths[path] {
				return nil, nil
			}
		}
	}
	directoryAfter, e := nativeSnapshot(projectRoot)
	if e != nil || !sameSnapshot(directoryBefore, directoryAfter) {
		return nil, nil
	}
	if epoch != a.index.Epoch() || !a.index.ConsumerAvailable() || !sameStatus(before, a.index.Status()) {
		return nil, nil
	}
	return result, nil
}
func invalidFilterDate(o localindex.ContentQueryOptions) bool {
	return o.ModifiedAfterMs != nil && math.IsNaN(*o.ModifiedAfterMs) || o.ModifiedBeforeMs != nil && math.IsNaN(*o.ModifiedBeforeMs)
}
