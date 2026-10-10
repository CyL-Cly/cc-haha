package providerconfig

import (
	"context"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

// Desktop selection uses exact advertised IDs, then the current default or
// first advertised row. This differs from the CLI's compatibility aliases.
func grokSelectionModel(model string, catalog []providerproxy.GrokModel) providerproxy.GrokModel {
	for _, entry := range catalog {
		if entry.Value == model {
			return entry
		}
	}
	for _, entry := range catalog {
		if entry.Value == providerproxy.GrokDefaultMainModel {
			return entry
		}
	}
	if len(catalog) > 0 {
		return catalog[0]
	}
	return providerproxy.GrokModel{Value: providerproxy.GrokDefaultMainModel}
}
func grokRuntimeEntry(ctx context.Context, options Options, model string) (providerproxy.GrokModel, error) {
	if options.GrokModelCatalog == nil {
		return providerproxy.GrokModel{}, unavailable("Go desktop Grok model catalog consumer is unavailable")
	}
	catalog, err := options.GrokModelCatalog(ctx)
	if err != nil {
		return providerproxy.GrokModel{}, err
	}
	return grokSelectionModel(model, catalog), nil
}
func normalizeGrokSelection(ctx context.Context, options Options, id *string, model string, requested *string) (RuntimeSelection, error) {
	entry, err := grokRuntimeEntry(ctx, options, model)
	if err != nil {
		return RuntimeSelection{}, err
	}
	selection := RuntimeSelection{Selection: Selection{ProviderID: id, ModelID: entry.Value}}
	if requested == nil {
		return selection, nil
	}
	entry, err = grokRuntimeEntry(ctx, options, entry.Value)
	if err != nil {
		return RuntimeSelection{}, err
	}
	if !slices.Contains(entry.ReasoningEfforts, *requested) {
		return RuntimeSelection{}, invalidSelection("effort")
	}
	selection.EffortLevel = *requested
	return selection, nil
}
