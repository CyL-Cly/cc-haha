package releasenotes

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestChangelogActualSourceParsingSortingAndBunSemver(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	contents := []string{"", "# Heading\n## 2.0.0\n- new\n## 1.0.0 - 2020-01-01\n - old \n- \n", "## 1.2.3\n- first\n## 1.2.3\n- replaced\n## v1.2.3\n- equal\n", "## 1\n- wildcard\n## 1.2\n- wildcard two\n## 1.2.3-alpha.01\n- alpha\n## 1.2.3\n- stable\n", "## invalid\n- x\n## 1.0.0\n- y\n", "## invalid\n- single invalid is returned without comparisons\n", "## __proto__\n- prototype setter\n## constructor\n- normal own property\n", "## 10\n- ten\n## 2\n- two\n## 01\n- one\n", "## 2.0.0\r\n - CRLF\r\n## 1.0.0\r\n- old\r\n", "preamble\u2028## 1.0.0\n- line terminator\n", "## 1.2.3-foo\n- foo\n## 1.2.3-2\n- two\n## 1.2.3-10\n- ten\n", "## 1.0.0\n-\tignored\n* ignored\n+ ignored\n-\u00a0ignored\n- \ufeffvisible\ufeff\n"}
	pairs := [][2]string{{"1", "1.2.3"}, {"1.2", "1.2.3"}, {"1.2.x", "1.2.3"}, {"1.x", "1.2.3"}, {"*", "1"}, {"=1.2.3", "v1.2.3"}, {"01.2.3", "1.2.3"}, {"1.2.3-alpha.01", "1.2.3-alpha.1"}, {"1.2.3-1", "1.2.3-a"}, {"1.2.3foo", "1.2.3-foo"}, {"1.2.3 - date", "1.2.3"}, {"1.2.3.4", "1.2.3"}, {"V1.2.3", "1.2.3"}, {"", "1.2.3"}, {"4294967296.1.1", "0.1.1"}, {"18446744073709551615.1.1", "1.1.1"}, {"1.2.3+foo", "1.2.3"}, {"1.2.3-foo", "1.2.3-Foo"}}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/release-notes.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_RELEASE_NOTES_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	data, _ := json.Marshal(map[string]any{"home": home, "contents": contents, "pairs": pairs})
	cmd.Stdin = bytes.NewReader(data)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(raw))
	}
	var result struct {
		Output []struct {
			Parsed map[string][]string
			All    []json.RawMessage
		}
		Orders []*int
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err, string(raw))
	}
	for i, content := range contents {
		parsed := map[string][]string{}
		for _, entry := range Parse(content) {
			parsed[entry.Version] = entry.Notes
		}
		if !reflect.DeepEqual(parsed, result.Output[i].Parsed) {
			t.Errorf("parsed %d Go%v Source%v", i, parsed, result.Output[i].Parsed)
		}
		var got [][2]any = make([][2]any, 0)
		for _, entry := range All(content) {
			got = append(got, [2]any{entry.Version, entry.Notes})
		}
		actual, _ := json.Marshal(got)
		expected, _ := json.Marshal(result.Output[i].All)
		if !bytes.Equal(actual, expected) {
			t.Errorf("all %d Go%s Source%s", i, actual, expected)
		}
	}
	for i, pair := range pairs {
		got, err := Compare(pair[0], pair[1])
		want := result.Orders[i]
		if want == nil && err == nil || want != nil && (err != nil || *want != got) {
			t.Errorf("order %v Go%d/%v Source%v", pair, got, err, want)
		}
	}
}
