package modelaccounting

import (
	"math"
	"strconv"
)

// NativeModelPricingString formats the existing Source pricing recipe. Model
// canonicalization and the caller's unknown-model fallback remain outside it.
func NativeModelPricingString(canonical string) (string, bool) {
	price, found := nativeModelCosts[canonical]
	if !found {
		return "", false
	}
	format := func(value float64) string {
		precision := 2
		if math.Trunc(value) == value {
			precision = 0
		}
		return "$" + strconv.FormatFloat(value, 'f', precision, 64)
	}
	return format(price.input) + "/" + format(price.output) + " per Mtok", true
}
