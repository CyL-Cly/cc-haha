package activitystatsapi

import (
	"regexp"
	"strings"
)

type costs struct{ input, output, write, read, search float64 }
type tier struct {
	prefix string
	price  costs
}

var tier315 = costs{3, 15, 3.75, .3, .01}
var tier210 = costs{2, 10, 2.5, .2, .01}
var tier1575 = costs{15, 75, 18.75, 1.5, .01}
var tier525 = costs{5, 25, 6.25, .5, .01}
var tier1050 = costs{10, 50, 12.5, 1, .01}
var tiers = []tier{
	{"claude-opus-5-5", costs{4, 20, 5, .2, .01}}, {"claude-opus-5", tier525},
	{"claude-opus-4-8", tier525}, {"claude-opus-4-7", tier525}, {"claude-opus-4-6", tier525}, {"claude-opus-4-5", tier525},
	{"claude-opus-4-1", tier1575}, {"claude-opus-4", tier1575}, {"claude-fable-5", tier1050}, {"claude-fable-5-1", costs{10, 50, 12.5, .25, .01}},
	{"claude-mythos-5", tier1050}, {"claude-sonnet-5-5", tier210}, {"claude-sonnet-5", tier210},
	{"claude-sonnet-4-6", tier315}, {"claude-sonnet-4-5", tier315}, {"claude-sonnet-4", tier315}, {"claude-3-7-sonnet", tier315}, {"claude-3-5-sonnet", tier315},
	{"claude-haiku-4-5", costs{1, 5, 1.25, .1, .01}}, {"claude-3-5-haiku", costs{.8, 4, 1, .08, .01}}, {"claude-3-haiku", costs{.8, 4, 1, .08, .01}}, {"claude-3-opus", tier1575},
}
var fastTiers = []tier{{"claude-opus-5-5", costs{8, 40, 10, .4, .01}}, {"claude-opus-5", tier1050}, {"claude-opus-4-8", tier1050}, {"claude-opus-4-7", costs{30, 150, 37.5, 3, .01}}}
var datedModel = regexp.MustCompile("-\\d{8}$")
var decoratedModel = regexp.MustCompile("-(r|thinking|latest)$")

func resolveCosts(model, speed string) *costs {
	model = strings.ToLower(trim(model))
	model = model[strings.LastIndex(model, "/")+1:]
	model = decoratedModel.ReplaceAllString(datedModel.ReplaceAllString(model, ""), "")
	match := func(list []tier) *costs {
		length := 0
		var result *costs
		for _, entry := range list {
			if len(entry.prefix) <= length {
				continue
			}
			if model == entry.prefix || strings.HasPrefix(model, entry.prefix+"-") {
				p := entry.price
				result = &p
				length = len(entry.prefix)
			}
		}
		return result
	}
	if speed == "fast" {
		if price := match(fastTiers); price != nil {
			return price
		}
	}
	return match(tiers)
}
func estimateCost(model string, tokens object, speed string) (float64, bool) {
	price := resolveCosts(model, speed)
	if price == nil {
		return 0, false
	}
	// Each ECMAScript multiplication rounds before the next addition. Native
	// arm64 fused multiply-add otherwise changes the last bit of persisted dollars.
	components := []float64{
		jsMultiply(numberValue(get(tokens, "inputTokens"))/1000000, price.input),
		jsMultiply(numberValue(get(tokens, "outputTokens"))/1000000, price.output),
		jsMultiply(numberValue(get(tokens, "cacheReadInputTokens"))/1000000, price.read),
		jsMultiply(numberValue(get(tokens, "cacheCreationInputTokens"))/1000000, price.write),
		jsMultiply(numberValue(orZero(get(tokens, "webSearchRequests"))), price.search),
	}
	total := components[0]
	for _, component := range components[1:] {
		total += component
	}
	return total, true
}

//go:noinline
func jsMultiply(left, right float64) float64 { return left * right }
