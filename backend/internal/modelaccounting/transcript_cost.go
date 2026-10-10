package modelaccounting

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

type TranscriptCost struct {
	CostUSD             float64
	HasUnknownModelCost bool
	DisplayName         string
	MaxOutputTokens     float64
}

// TranscriptPricing shares the native pricing recipes and model resolvers. It
// does not enter or update a live session ledger; a transcript may contain the
// empty string model that the normal model-start constructor cannot admit.
func TranscriptPricing(model string, usage protocol.JSONObject, o NativeCalculatorOptions) (TranscriptCost, error) {
	a := &Calculator{Model: model, Models: protocol.NewJSONObject(), native: &o}
	amount, err := a.Charge(usage)
	if err != nil {
		return TranscriptCost{}, err
	}
	resolver := RestoredModelResolver{Environment: o.Environment, Settings: o.Settings, Global: o.Global, SDKBetas: o.SDKBetas}
	name, err := resolver.Canonical(model)
	if err != nil {
		return TranscriptCost{}, err
	}
	row := object(a.Models.Get(model))
	return TranscriptCost{amount, a.UnknownModel, name, num(row.Get("maxOutputTokens"))}, nil
}

func FormatUsageCost(amount float64) string { return costText(amount) }
