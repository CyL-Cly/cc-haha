package localindex

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type PrimaryDiscoveryResult struct {
	Complete    bool `json:"complete"`
	RootMissing bool `json:"rootMissing,omitempty"`
}
type ActivityCandidate struct {
	SessionCandidate
	IsSubagent bool `json:"isSubagent"`
}
type ActivityDiscovery struct {
	PrimaryDiscoveryResult
	Candidates []ActivityCandidate `json:"candidates"`
}

func desanitizeProject(project string) string {
	if len(project) >= 3 && ((project[0] >= 'a' && project[0] <= 'z') || (project[0] >= 'A' && project[0] <= 'Z')) && project[1:3] == "--" {
		return project[:1] + ":\\" + strings.ReplaceAll(project[3:], "-", "\\")
	}
	return strings.ReplaceAll(project, "-", string(filepath.Separator))
}
func nativeDirectoryEntries(path string) ([]os.DirEntry, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return f.ReadDir(-1)
}
func candidateFromFile(path, id, project string, metadata func(string) (SourceMetadata, error)) (SessionCandidate, error) {
	info, e := os.Stat(path)
	if e != nil {
		return SessionCandidate{}, e
	}
	m, e := metadata(path)
	if e != nil {
		return SessionCandidate{}, e
	}
	workdir := desanitizeProject(project)
	return SessionCandidate{path, id, project, m.BirthtimeISO, m.MtimeISO, &workdir, float64(info.ModTime().Unix())*1000 + float64(info.ModTime().Nanosecond())/1e6}, nil
}
func sortCandidates(candidates []SessionCandidate, compare func(string, string) int) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		return a.ModifiedAtMs > b.ModifiedAtMs || a.ModifiedAtMs == b.ModifiedAtMs && compare(a.Path, b.Path) < 0
	})
}
func DiscoverTranscriptSources(ctx context.Context, scope string, metadata func(string) (SourceMetadata, error), compare func(string, string) int, emit func([]SessionCandidate) error) (PrimaryDiscoveryResult, error) {
	projects := filepath.Join(scope, "projects")
	dirs, e := nativeDirectoryEntries(projects)
	if os.IsNotExist(e) {
		return PrimaryDiscoveryResult{Complete: true, RootMissing: true}, nil
	}
	if e != nil {
		return PrimaryDiscoveryResult{}, e
	}
	complete := true
	pending := []SessionCandidate{}
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		sortCandidates(pending, compare)
		batch := pending
		pending = nil
		return emit(batch)
	}
	for _, dir := range dirs {
		if ctx.Err() != nil {
			return PrimaryDiscoveryResult{Complete: false}, nil
		}
		if !dir.IsDir() {
			continue
		}
		project := filepath.Join(projects, dir.Name())
		files, e := nativeDirectoryEntries(project)
		if os.IsNotExist(e) {
			complete = false
			continue
		}
		if e != nil {
			return PrimaryDiscoveryResult{}, e
		}
		for _, entry := range files {
			if ctx.Err() != nil {
				return PrimaryDiscoveryResult{Complete: false}, nil
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(project, entry.Name())
			candidate, e := candidateFromFile(path, strings.TrimSuffix(entry.Name(), ".jsonl"), dir.Name(), metadata)
			if os.IsNotExist(e) {
				complete = false
				continue
			}
			if e != nil {
				return PrimaryDiscoveryResult{}, e
			}
			pending = append(pending, candidate)
			if len(pending) >= 25 {
				if e = flush(); e != nil {
					return PrimaryDiscoveryResult{}, e
				}
			}
		}
	}
	if e = flush(); e != nil {
		return PrimaryDiscoveryResult{}, e
	}
	return PrimaryDiscoveryResult{Complete: complete}, nil
}
func collectSubagentPaths(ctx context.Context, path string, depth int) ([]string, bool) {
	entries, e := nativeDirectoryEntries(path)
	if os.IsNotExist(e) {
		return []string{}, true
	}
	if e != nil {
		return []string{}, false
	}
	paths := []string{}
	complete := true
	for _, entry := range entries {
		if ctx.Err() != nil {
			return paths, false
		}
		child := filepath.Join(path, entry.Name())
		if entry.Type().IsRegular() {
			if strings.HasPrefix(entry.Name(), "agent-") && strings.HasSuffix(entry.Name(), ".jsonl") {
				paths = append(paths, child)
			}
		} else if entry.IsDir() && depth < 3 {
			nested, ok := collectSubagentPaths(ctx, child, depth+1)
			paths = append(paths, nested...)
			complete = complete && ok
		}
	}
	return paths, complete
}
func DiscoverActivitySources(ctx context.Context, scope string, metadata func(string) (SourceMetadata, error), compare func(string, string) int) (ActivityDiscovery, error) {
	projects := filepath.Join(scope, "projects")
	dirs, e := nativeDirectoryEntries(projects)
	if os.IsNotExist(e) {
		return ActivityDiscovery{PrimaryDiscoveryResult{true, true}, []ActivityCandidate{}}, nil
	}
	if e != nil {
		return ActivityDiscovery{}, e
	}
	out := ActivityDiscovery{PrimaryDiscoveryResult{Complete: true}, []ActivityCandidate{}}
	add := func(path, id, project string, sub bool) error {
		candidate, e := candidateFromFile(path, id, project, metadata)
		if os.IsNotExist(e) {
			out.Complete = false
			return nil
		}
		if e != nil {
			return e
		}
		out.Candidates = append(out.Candidates, ActivityCandidate{candidate, sub})
		return nil
	}
	for _, dir := range dirs {
		if ctx.Err() != nil {
			out.Complete = false
			return out, nil
		}
		if !dir.IsDir() {
			continue
		}
		project := filepath.Join(projects, dir.Name())
		entries, e := nativeDirectoryEntries(project)
		if os.IsNotExist(e) {
			out.Complete = false
			continue
		}
		if e != nil {
			return ActivityDiscovery{}, e
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				out.Complete = false
				return out, nil
			}
			path := filepath.Join(project, entry.Name())
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
				if e = add(path, strings.TrimSuffix(entry.Name(), ".jsonl"), dir.Name(), false); e != nil {
					return ActivityDiscovery{}, e
				}
			} else if entry.IsDir() {
				paths, complete := collectSubagentPaths(ctx, filepath.Join(path, "subagents"), 1)
				out.Complete = out.Complete && complete
				for _, path := range paths {
					if e = add(path, entry.Name(), dir.Name(), true); e != nil {
						return ActivityDiscovery{}, e
					}
				}
			}
		}
	}
	sort.SliceStable(out.Candidates, func(i, j int) bool {
		a, b := out.Candidates[i], out.Candidates[j]
		return a.ModifiedAtMs > b.ModifiedAtMs || a.ModifiedAtMs == b.ModifiedAtMs && compare(a.Path, b.Path) < 0
	})
	return out, nil
}
