package diagnostics

import (
	"math"
	"testing"
)

func TestDiagnosticsNumberPreservesNonFiniteAndNegativeZeroJSON(t *testing.T) {
	for _, item := range []struct {
		value float64
		want  string
	}{
		{math.NaN(), "null"}, {math.Inf(1), "null"}, {math.Inf(-1), "null"},
		{math.Copysign(0, -1), "0"}, {1.5, "1.5"}, {-10, "-10"},
	} {
		if got := string(Number(item.value)); got != item.want {
			t.Fatalf("%v: %s, want %s", item.value, got, item.want)
		}
	}
}
