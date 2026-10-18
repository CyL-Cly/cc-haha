package textsanitization

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestFullUnicodeAndRecursiveStructuresAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	values := []json.RawMessage{[]byte(`""`), []byte(`"ＡＢＣ ℌ ① ﬀ \u200b \ue000 😀"`), []byte(`"e\u0301\ud800x\udfff"`), []byte(`{"＿name":"value\u200d","name":"first","ｎａｍｅ":"second","__proto__":{"outside":true},"prototype":[null,true,2,"\ud800"],"nested":{"Å":"\u2066text"}}`), []byte(`"\udfff\ud800\u31ef\ud87a\udff0"`),
		// The OS normalizer and Bun's property tables can have different Unicode
		// versions. These strings exercise expansion beyond the initial UTF-16
		// buffer, Unicode 16 composition, and a Unicode 17 compatibility letter
		// beside combining/lone-surrogate boundaries through the actual Source.
		[]byte(`"\ufdfa\ufdfa e\u0301 \ud800 \ua7f1\u0301 \ud81b\udd68\ud81b\udd69\ud81b\udd6a \udfff"`)}
	digest := sha256.New()
	var diagnostic bytes.Buffer
	for code := 0; code <= 0x10ffff; code++ {
		var units []uint16
		if code >= 0xd800 && code <= 0xdfff {
			units = []uint16{uint16(code)}
		} else {
			units = utf16.Encode([]rune{rune(code)})
		}
		value, err := PartiallySanitize(protocol.QuoteJavaScriptUTF16(units))
		if err != nil {
			t.Fatalf("code %x: %v", code, err)
		}
		_, _ = digest.Write(value)
		_, _ = digest.Write([]byte("\n"))
		diagnostic.Write(value)
		diagnostic.WriteByte('\n')
	}
	actual := []json.RawMessage{}
	for _, value := range values {
		next, err := Recursive(value)
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, next)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-text-sanitization.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_TEXT_SANITIZATION_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(values)
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	expected, err := command.Output()
	if err != nil {
		t.Fatalf("Source Unicode: %v %s", err, stderr.String())
	}
	var result struct {
		Unicode, Digest string
		Values          []json.RawMessage
	}
	if err := json.Unmarshal(expected, &result); err != nil {
		t.Fatal(err)
	}
	if result.Unicode != "15.1" {
		t.Fatalf("Source Unicode changed to %s; update assigned-category declarations and rerun parity", result.Unicode)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != result.Digest {
		t.Errorf("exhaustive single-codepoint digest differs Go=%s Source=%s", got, result.Digest)
		if err := os.WriteFile(filepath.Join(home, "go-unicode.jsonl"), diagnostic.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		debugInput, _ := json.Marshal(map[string]any{"debug": true, "values": values})
		debug := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		debug.Dir = command.Dir
		debug.Env = command.Env
		debug.Stdin = bytes.NewReader(debugInput)
		output, err := debug.Output()
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(output))
	}
	a, b := []any{}, []any{}
	for _, value := range actual {
		var o any
		_ = json.Unmarshal(value, &o)
		a = append(a, o)
	}
	for _, value := range result.Values {
		var o any
		_ = json.Unmarshal(value, &o)
		b = append(b, o)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("recursive Go=%s Source=%s", input, expected)
	}
}

func TestRecursiveSanitizationKeepsSourceIdentityAndLoneUnits(t *testing.T) {
	result, err := Recursive([]byte(`{"Ａ":"①","A":"second","nested":["\ud800","\ue000\u200cOK"],"__proto__":{"x":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"A":"second","nested":["\ud800","OK"]}` {
		t.Fatal(string(result))
	}
	if _, err := Recursive([]byte(`{`)); err == nil {
		t.Fatal("invalid JSON admitted")
	}
}
