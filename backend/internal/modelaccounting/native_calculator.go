package modelaccounting

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type NativeCalculatorOptions struct {
	Environment      map[string]string
	Settings, Global func() (*protocol.JSONObject, error)
	SDKBetas         []string
	ModelSelector    func(modelselection.Options) (*modelselection.Selector, error)
}

type nativeCosts struct{ input, output, write, read, search float64 }

// Literal frozen Source modelCost.ts pricing recipes. Request fast-mode
// admission remains with the Controller; pricing uses the actual usage speed.
var nativeModelCosts = map[string]nativeCosts{
	"claude-opus-5-5":   {4, 20, 5, 0.2, 0.01},
	"claude-opus-5":     {5, 25, 6.25, 0.5, 0.01},
	"claude-fable-5-1":  {10, 50, 12.5, 0.25, 0.01},
	"claude-fable-5":    {10, 50, 12.5, 1, 0.01},
	"claude-3-5-haiku":  {0.8, 4, 1, 0.08, 0.01},
	"claude-haiku-4-5":  {1, 5, 1.25, 0.1, 0.01},
	"claude-3-5-sonnet": {3, 15, 3.75, 0.3, 0.01},
	"claude-3-7-sonnet": {3, 15, 3.75, 0.3, 0.01},
	"claude-sonnet-4":   {3, 15, 3.75, 0.3, 0.01},
	"claude-sonnet-4-5": {3, 15, 3.75, 0.3, 0.01},
	"claude-sonnet-4-6": {3, 15, 3.75, 0.3, 0.01},
	"claude-sonnet-5-5": {2, 10, 2.5, 0.2, 0.01},
	"claude-sonnet-5":   {2, 10, 2.5, 0.2, 0.01},
	"claude-opus-4":     {15, 75, 18.75, 1.5, 0.01},
	"claude-opus-4-1":   {15, 75, 18.75, 1.5, 0.01},
	"claude-opus-4-5":   {5, 25, 6.25, 0.5, 0.01},
	"claude-opus-4-7":   {5, 25, 6.25, 0.5, 0.01},
	"claude-opus-4-8":   {5, 25, 6.25, 0.5, 0.01},
}

func NewNativeCalculator(model string, o NativeCalculatorOptions) (*Calculator, error) {
	if o.Environment == nil || o.Settings == nil || o.Global == nil {
		return nil, errors.New("native accounting requires original model/environment/settings/global getters")
	}
	// Source's application-inference-profile backing model needs its real AWS
	// resolver; ordinary model IDs and inference profile IDs are self contained.
	if envTruthy(o.Environment["CLAUDE_CODE_USE_BEDROCK"]) && strings.Contains(model, "application-inference-profile") {
		return nil, unsupported("native accounting Bedrock application profile backing model")
	}
	for _, key := range []string{"__proto__", "constructor", "toString", "valueOf", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__"} {
		if model == key {
			return nil, unsupported("native accounting inherited object ledger key")
		}
	}
	o.Environment = copyEnvironment(o.Environment)
	o.SDKBetas = append([]string{}, o.SDKBetas...)
	return &Calculator{Model: model, Models: protocol.NewJSONObject(), native: &o}, nil
}

func (a *Calculator) resolveNativePrice(usage protocol.JSONObject) (nativeCosts, bool, float64, float64, error) {
	o := a.native
	settings, err := o.Settings()
	if err != nil {
		return nativeCosts{}, false, 0, 0, err
	}
	merged := protocol.NewJSONObject()
	if settings != nil {
		merged = settings.Clone()
	}
	selectModel := o.ModelSelector
	if selectModel == nil {
		selectModel = modelselection.NewDirectCredentials
	}
	s, err := selectModel(modelselection.Options{Environment: o.Environment, Settings: merged})
	if err != nil {
		return nativeCosts{}, false, 0, 0, err
	}
	canonical, err := s.Canonical(quote(a.Model))
	if err != nil {
		return nativeCosts{}, false, 0, 0, err
	}
	var name string
	if err = json.Unmarshal(canonical, &name); err != nil {
		return nativeCosts{}, false, 0, 0, err
	}
	price, known := nativeModelCosts[name]
	if name == "claude-opus-4-7" && !envTruthy(o.Environment["CLAUDE_CODE_DISABLE_FAST_MODE"]) && usage.String("speed") == "fast" {
		price = nativeCosts{30, 150, 37.5, 3, 0.01}
	}
	if !known {
		fallback, err := s.Canonical(s.DefaultSetting())
		if err != nil {
			return nativeCosts{}, false, 0, 0, err
		}
		if err = json.Unmarshal(fallback, &name); err != nil {
			return nativeCosts{}, false, 0, 0, err
		}
		var found bool
		price, found = nativeModelCosts[name]
		if !found {
			price = nativeCosts{5, 25, 6.25, 0.5, 0.01}
		}
	}
	metrics := RestoredModelResolver{Environment: o.Environment, Settings: o.Settings, Global: o.Global, SDKBetas: o.SDKBetas}
	window, output, err := metrics.Resolve(a.Model)
	return price, !known, window, output, err
}
