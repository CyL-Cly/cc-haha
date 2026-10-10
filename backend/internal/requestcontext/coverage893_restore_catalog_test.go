package requestcontext

import "testing"

func TestCoverage893RestoredOutputLimitsAcrossConfiguredModelNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		want float64
	}{{"fixture-model", 32000}, {"claude-opus-5-5", 128000}, {"claude-sonnet-5-5", 128000}, {"vendor-fable-5", 64000}, {"vendor-opus-4-8", 64000}, {"vendor-sonnet-4-6", 32000}, {"claude-3-opus-custom", 4096}, {"claude-3-haiku-custom", 4096}, {"claude-3-sonnet-custom", 8192}, {"vendor-3-5-haiku", 8192}, {"future-model", 32000}} {
		t.Run(tc.name, func(t *testing.T) {
			n, e := RestoreDefaultOutputTokens(tc.name)
			if e != nil || n != tc.want {
				t.Fatal(n, e)
			}
		})
	}
}
