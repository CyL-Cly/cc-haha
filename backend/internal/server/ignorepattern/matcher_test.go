package ignorepattern

import (
	"sync"
	"testing"
)

func TestPinnedGrammarAndNestedNegation(t *testing.T) {
	matcher, err := New("darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err := matcher.Add("# comment\n*.log\n!important.log\n/root.txt\ncache/\n\\#literal\ntrailing\\ \na/**/z?.txt\n[ab].tmp\n"); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		path               string
		ignored, unignored bool
	}{
		{"x.log", true, false}, {"src/x.log", true, false}, {"important.log", false, true}, {"src/important.log", false, true},
		{"root.txt", true, false}, {"src/root.txt", false, false}, {"cache/", true, false}, {"cache/keep.txt", true, false},
		{"#literal", true, false}, {"trailing ", true, false}, {"trailing", false, false}, {"a/z1.txt", true, false},
		{"a/deep/z2.txt", true, false}, {"a/deep/z22.txt", false, false}, {"b.tmp", true, false}, {"c.tmp", false, false},
	} {
		result, err := matcher.Test(sample.path)
		if err != nil || result.Ignored != sample.ignored || result.Unignored != sample.unignored {
			t.Errorf("%q: %+v %v", sample.path, result, err)
		}
	}
	if _, err := matcher.Test("/absolute"); err == nil {
		t.Fatal("absolute pattern path was accepted")
	}
}

func TestMatcherSerializesAccessAndKeepsRuntimesSeparate(t *testing.T) {
	matcher, err := New("linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := matcher.Add("*.secret"); err != nil {
		t.Fatal(err)
	}
	other, err := New("linux")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := matcher.Test("nested/file.secret")
			if err != nil || !result.Ignored {
				t.Error(result, err)
			}
		}()
	}
	group.Wait()
	result, err := other.Test("nested/file.secret")
	if err != nil || result.Ignored {
		t.Fatal("language runtime leaked", result, err)
	}
}

func TestWindowsPathLanguageInput(t *testing.T) {
	matcher, err := New("win32")
	if err != nil {
		t.Fatal(err)
	}
	if err := matcher.Add("dir/*.txt"); err != nil {
		t.Fatal(err)
	}
	result, err := matcher.Test(`dir\file.txt`)
	if err != nil || !result.Ignored {
		t.Fatal(result, err)
	}
	if _, err := matcher.Test(`C:\file.txt`); err == nil {
		t.Fatal("Windows absolute path was accepted")
	}
}

func TestRuleObservationWithoutMatchingRuleKeepsFileTraversalUsable(t *testing.T) {
	matcher, err := New("darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err = matcher.Add("*.secret\n!important.secret\n"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"ordinary.txt", "nested/ordinary.txt", "important.secret"} {
		result, pattern, err := matcher.TestRule(path)
		if err != nil || result.Ignored || pattern != "" {
			t.Fatalf("%s: %+v pattern=%q err=%v", path, result, pattern, err)
		}
	}
	result, pattern, err := matcher.TestRule("nested/file.secret")
	if err != nil || !result.Ignored || pattern != "*.secret" {
		t.Fatal(result, pattern, err)
	}
}
