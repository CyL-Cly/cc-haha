package sdkpermissions

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
)

func TestNativeConsumerSettingsRequireActualBindings(t *testing.T) {
	for _, row := range []struct {
		settings string
		missing  []int
	}{
		{`{"claudeMdExcludes":["**/CLAUDE.md"]}`, []int{5, 10}},
		{`{"autoMemoryEnabled":false}`, []int{5, 10}},
		{`{"plansDirectory":"custom/plans"}`, []int{14}},
		{`{"spinnerVerbs":{"mode":"replace","verbs":["验证"]}}`, []int{15}},
		{`{"showThinkingSummaries":true}`, []int{16}},
		{`{"showThinkingSummaries":false}`, []int{16}},
		{`{"autoDreamEnabled":true}`, []int{17}},
		{`{"autoDreamEnabled":false}`, []int{17}},
		{`{"companyAnnouncements":["实际团队公告"]}`, []int{2}},
	} {
		parsed, err := clisettings.ValidateNativeJSONContent(row.settings, "fixture")
		if err != nil || parsed.Settings == nil {
			t.Fatal(err, parsed)
		}
		flags := make([]bool, 18)
		for i := range flags {
			flags[i] = true
		}
		if err = ValidateNativeWithBindings(parsed.Settings, false, false, flags...); err != nil {
			t.Fatal("bound actual consumer rejected", row.settings, err)
		}
		for _, missing := range row.missing {
			flags[missing] = false
			if err = ValidateNativeWithBindings(parsed.Settings, false, false, flags...); err == nil {
				t.Fatal("unbound consumer admitted", row.settings, missing)
			}
			flags[missing] = true
		}
		if err = ValidateNative(parsed.Settings); err == nil {
			t.Fatal("unproven standalone consumer admitted", row.settings)
		}
	}
}
