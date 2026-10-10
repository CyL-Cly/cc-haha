package builtinwrite

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStructuredPatchRetainsDiffJSContextAndMissingNewline(t *testing.T) {
	for _, tc := range []struct{ name, old, new, want string }{
		{"replace", "a\nb\nc\n", "a\nB\nc\n", `[{"oldStart":1,"oldLines":3,"newStart":1,"newLines":3,"lines":[" a","-b","+B"," c"]}]`},
		{"no-newline", "old", "new", `[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-old","\\ No newline at end of file","+new","\\ No newline at end of file"]}]`},
		{"append", "a\n", "a\nb\n", `[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":2,"lines":[" a","+b"]}]`},
		{"same", "same\n", "same\n", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := StructuredPatch(tc.old, tc.new)
			var want []PatchHunk
			if e := json.Unmarshal([]byte(tc.want), &want); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("patch %#v want %#v", got, want)
			}
		})
	}
}
