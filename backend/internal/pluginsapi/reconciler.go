package pluginsapi

import (
	"context"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

type MarketplaceSourceChange struct {
	Name               string              `json:"name"`
	DeclaredSource     protocol.JSONObject `json:"declaredSource"`
	MaterializedSource protocol.JSONObject `json:"materializedSource"`
}
type MarketplaceDiff struct {
	Missing       []string                  `json:"missing"`
	SourceChanged []MarketplaceSourceChange `json:"sourceChanged"`
	UpToDate      []string                  `json:"upToDate"`
}
type MarketplaceFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}
type ReconcileProgress struct {
	Type                string `json:"type"`
	Name                string `json:"name"`
	Action              string `json:"action,omitempty"`
	Index               int    `json:"index,omitempty"`
	Total               int    `json:"total,omitempty"`
	AlreadyMaterialized *bool  `json:"alreadyMaterialized,omitempty"`
	Error               string `json:"error,omitempty"`
}
type ReconcileResult struct {
	Installed []string             `json:"installed"`
	Updated   []string             `json:"updated"`
	Failed    []MarketplaceFailure `json:"failed"`
	UpToDate  []string             `json:"upToDate"`
	Skipped   []string             `json:"skipped"`
}
type ReconcileOptions struct {
	Skip       func(string, protocol.JSONObject) bool
	OnProgress func(ReconcileProgress)
}

func NormalizeMarketplaceSource(source protocol.JSONObject, projectRoot string, roots ...*MarketplaceRoots) protocol.JSONObject {
	next := source.Clone()
	if (source.String("source") == "directory" || source.String("source") == "file") && !filepath.IsAbs(source.String("path")) {
		base := ""
		if len(roots) > 0 && roots[0] != nil {
			base = roots[0].Canonical(projectRoot)
		} else {
			base = usercontext.CanonicalGitRoot(usercontext.OSMemoryFiles{}, filepath.EvalSymlinks, projectRoot)
		}
		if base == "" {
			base = projectRoot
		}
		next.Set("path", raw(filepath.Join(base, source.String("path"))))
	}
	return next
}

func DiffMarketplaces(declared, materialized protocol.JSONObject, projectRoot string, roots ...*MarketplaceRoots) MarketplaceDiff {
	diff := MarketplaceDiff{Missing: []string{}, SourceChanged: []MarketplaceSourceChange{}, UpToDate: []string{}}
	for _, name := range properties(declared) {
		intent, state := obj(declared.Get(name)), obj(materialized.Get(name))
		source := NormalizeMarketplaceSource(obj(intent.Get("source")), projectRoot, roots...)
		if materialized.Get(name) == nil {
			diff.Missing = append(diff.Missing, name)
		} else if string(intent.Get("sourceIsFallback")) == "true" || equalJSON(source, obj(state.Get("source"))) {
			diff.UpToDate = append(diff.UpToDate, name)
		} else {
			diff.SourceChanged = append(diff.SourceChanged, MarketplaceSourceChange{name, source, obj(state.Get("source"))})
		}
	}
	return diff
}

// Reconcile is additive and observes actual source fetches. Local source
// changes whose path has disappeared preserve the existing usable mapping;
// missing local mappings still fail and produce an actionable status.
func (m *MarketplaceManager) Reconcile(ctx context.Context, declared protocol.JSONObject, projectRoot string, options ReconcileOptions) (ReconcileResult, error) {
	result := ReconcileResult{Installed: []string{}, Updated: []string{}, Failed: []MarketplaceFailure{}, UpToDate: []string{}, Skipped: []string{}}
	if len(properties(declared)) == 0 {
		return result, nil
	}
	known, err := m.Known(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		known = rec()
	}
	diff := DiffMarketplaces(declared, known, projectRoot, m.options.Roots)
	result.UpToDate = diff.UpToDate
	type item struct {
		name, action string
		source       protocol.JSONObject
	}
	work := []item{}
	for _, name := range diff.Missing {
		work = append(work, item{name, "install", NormalizeMarketplaceSource(obj(obj(declared.Get(name)).Get("source")), projectRoot, m.options.Roots)})
	}
	for _, changed := range diff.SourceChanged {
		work = append(work, item{changed.Name, "update", changed.DeclaredSource})
	}
	selected := []item{}
	for _, current := range work {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		skip := options.Skip != nil && options.Skip(current.name, current.source)
		if !skip && current.action == "update" && (current.source.String("source") == "directory" || current.source.String("source") == "file") {
			_, statError := os.Stat(current.source.String("path"))
			skip = statError != nil
		}
		if skip {
			result.Skipped = append(result.Skipped, current.name)
		} else {
			selected = append(selected, current)
		}
	}
	emit := func(event ReconcileProgress) {
		if options.OnProgress != nil {
			options.OnProgress(event)
		}
	}
	for i, current := range selected {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		emit(ReconcileProgress{Type: "installing", Name: current.name, Action: current.action, Index: i + 1, Total: len(selected)})
		added, err := m.Materialize(ctx, current.source, nil)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.Failed = append(result.Failed, MarketplaceFailure{current.name, err.Error()})
			emit(ReconcileProgress{Type: "failed", Name: current.name, Error: err.Error()})
			continue
		}
		if current.action == "install" {
			result.Installed = append(result.Installed, current.name)
		} else {
			result.Updated = append(result.Updated, current.name)
		}
		emit(ReconcileProgress{Type: "installed", Name: current.name, AlreadyMaterialized: &added.AlreadyMaterialized})
	}
	return result, nil
}
