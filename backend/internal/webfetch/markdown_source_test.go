package webfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMarkdownDefaultRulesMatchActualSourceTurndown(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	samples := []string{"", " Hello world  ", "<h1>Title 😀</h1><h2>Sub</h2><h3>Third</h3><p>Paragraph &amp; entities</p>", "<p>bold <strong>with spaces </strong> and <em> italics </em>.</p>", "<p>line<br>break</p><hr><p>*[foo] _bar_ `code`</p>", "<a href='/a(b)<c> d' title='a &quot;title&quot;'> link </a><img src='/file(a).png' alt='[foo]_bar' title='title'>", "<ol start='3'><li>one</li><li><p>second paragraph</p><p>more</p></li><li><ul><li>nested</li></ul></li></ol>", "<ul><li> first <b>bold</b> </li><li></li><li>last</li></ul>", "<pre><code>line1\n  line2\n</code></pre>", "<pre> plain pre\nnext </pre><p><code>``a`b```</code> <code> x </code></p>", "<blockquote><p>quote</p><blockquote>nested</blockquote><p>end</p></blockquote>", "<table><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td>1</td><td>2</td></tr></tbody></table>", "<script>var x = 'hello';</script><style>body{color:red}</style><div><!--comment--></div>", "<p>&nbsp;<em> 😀 </em>&nbsp; end</p>", "<html><head><title>Document title</title></head><body><article><p>Nested body</p></article></body></html>", "<p>paragraph<p>unclosed<strong>bold", "<p>one<span> </span> two <img src='x'> text</p>", "<ol start='0x10'><li>hex start</li></ol>", "<a href='x' title='foo\n\u00a0\u2003bar'>link</a>", "<ol start='Inf'><li>invalid</li></ol>", "<ol start='Infinity'><li>infinity</li></ol>"}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-webfetch-markdown.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WEBFETCH_MARKDOWN_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "html": samples})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source markdown %v %.1800s", err, stderr.String())
	}
	var source struct {
		Markdown     []string
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Markdown) != len(samples) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped %v %.1000s", source.Capabilities.Blocked, out)
	}
	for i, html := range samples {
		actual, err := HTMLToMarkdown(html)
		if err != nil || actual != source.Markdown[i] {
			t.Errorf("case%d input%q\nGo%q %v\nSource%q", i, html, actual, err, source.Markdown[i])
		}
	}
}
