package sdkpermissions

import "testing"

func TestLSPExplicitCLIRequiresItsOwnNativeBinding(t *testing.T) {
	for _, name := range []string{"LSP", "LSP(*)", "LSP(hover)"} {
		if _, err := nativeToolList([]string{name}); err == nil {
			t.Fatal("finite CLI accepted LSP without concrete binding")
		}
		other := []bool{true, true, true, true, true, true, true, true, true, true, true, true, true, false}
		if _, err := nativeToolList([]string{name}, other...); err == nil {
			t.Fatal("another tool's binding admitted LSP")
		}
		other[13] = true
		if got, err := nativeToolList([]string{name}, other...); err != nil || len(got) != 1 {
			t.Fatal("actual LSP binding rejected", got, err)
		}
	}
}
