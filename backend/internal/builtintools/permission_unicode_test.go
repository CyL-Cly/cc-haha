package builtintools

import "testing"

func TestPermissionGlobPreservesUnicodeLiteralNames(t *testing.T) {
	for _, row := range []struct {
		pattern, path string
		want          bool
	}{{"**/code space 中文.txt", "code space 中文.txt", true}, {"**/code space 中文.txt", "nested/code space 中文.txt", true}, {"**/code space 中文.txt", "nested/code space English.txt", false}, {"**/🧪.go", "nested/🧪.go", true}, {"src/?.go", "src/中.go", true}, {"**/code[.]txt", "code.txt", true}} {
		if got := globMatch(row.pattern, row.path); got != row.want {
			t.Errorf("%q vs %q: %v, want %v", row.pattern, row.path, got, row.want)
		}
	}
}
