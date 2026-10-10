package retrypolicy

import (
	"math"
	"strings"
	"testing"
)

func TestRetryDirectiveSuppressesJitterAndBypassesCap(t *testing.T) {
	for _, scenario := range []struct {
		value string
		want  float64
	}{
		{"0", 500}, {"-25", 500}, {"10.9", 10000}, {"1e5", 1000},
		{"\ufeff\u00a0 +100seconds", 100000}, {"0x20", 500},
		{"9007199254740993", 9007199254740992 * 1000},
	} {
		t.Run(scenario.value, func(t *testing.T) {
			got := Delay(9, &scenario.value, 1, func() float64 { t.Fatal("numeric header consumed RNG"); return 0 })
			if got != scenario.want {
				t.Fatal(got, scenario.want)
			}
		})
	}
}

func TestFallbackBackoffUsesExactlyOneRandomSample(t *testing.T) {
	for _, header := range []*string{nil, stringPointer(""), stringPointer("tomorrow"), stringPointer(strings.Repeat("9", 400))} {
		calls := 0
		got := Delay(10, header, 32000, func() float64 { calls++; return 0.5 })
		if got != 36000 || calls != 1 {
			t.Fatal(got, calls)
		}
	}
	if got := Delay(0, nil, 32000, func() float64 { return 0.25 }); got != 265.625 {
		t.Fatal(got)
	}
	if got := Delay(math.NaN(), nil, 32000, func() float64 { return 0 }); !math.IsNaN(got) {
		t.Fatal(got)
	}
	if got := Delay(math.NaN(), nil, math.Inf(-1), func() float64 { return 0.5 }); !math.IsNaN(got) {
		t.Fatal("Go math.Min must not win over JavaScript NaN propagation", got)
	}
}

func TestRetryEnvironmentValuesRetainSourceParseIntAndSign(t *testing.T) {
	for _, scenario := range []struct {
		value string
		want  float64
	}{
		{"", 10}, {"-7tail", -7}, {"\u2000+12ms", 12}, {"0xFF", 0},
		{"9007199254740993", 9007199254740992},
	} {
		if got := DefaultMaximum(scenario.value); got != scenario.want {
			t.Fatal(scenario.value, got, scenario.want)
		}
	}
	for _, value := range []string{"+", "-", "\u0085 3", "not-a-number"} {
		if got := DefaultMaximum(value); !math.IsNaN(got) {
			t.Fatal(value, got)
		}
	}
	if got := DefaultMaximum("-0"); got != 0 || !math.Signbit(got) {
		t.Fatal("negative zero lost", got)
	}
	if got := DefaultMaximum(strings.Repeat("9", 400)); !math.IsInf(got, 1) {
		t.Fatal(got)
	}
	for _, scenario := range []struct {
		value string
		want  float64
	}{
		{"", 2}, {"no", 2}, {"-7", 2}, {strings.Repeat("9", 400), 2},
		{"100", 5}, {"\ufeff+2tail", 2}, {"0xFF", 0}, {"-0", math.Copysign(0, -1)},
	} {
		got := StreamTransientMaximum(scenario.value)
		if got != scenario.want || math.Signbit(got) != math.Signbit(scenario.want) {
			t.Fatal(scenario.value, got, scenario.want)
		}
	}
}

func stringPointer(value string) *string { return &value }
