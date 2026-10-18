package terminaldiff

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"testing"
)

func TestActualSourceDiffWordsWithSpaceUsesOriginalTokenizationAndMyersChoice(t *testing.T) {
	pairs := [][2]string{{"", ""}, {"same", "same"}, {"a b", "a  b"}, {"foo bar baz", "foo baz bar"}, {"return oldName(value)", "return newName(value)"}, {"éąŻ ÷× abc_1", "éąż ÷× abc_2"}, {"🙂中文\r\n\t x", "🙃中文\n\t  x"}, {"a a b a", "b a b b"}, {"one\u00a0two\ufeffthree", "one two three"}}
	source := originalDiff(t, map[string]any{"words": pairs})
	var results [][]builtinwrite.WordChange
	if e := json.Unmarshal(source.Get("words"), &results); e != nil {
		t.Fatal(e)
	}
	for i, pair := range pairs {
		got := builtinwrite.WordDiff(pair[0], pair[1])
		if !jsonSame(got, results[i]) {
			t.Fatalf("case %d\nGo %+v\nSource %+v", i, got, results[i])
		}
	}
}
