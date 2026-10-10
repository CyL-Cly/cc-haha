package imregistration

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func runQQSource(t *testing.T, fixtureName, flag string, input any, output any) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/" + fixtureName)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(input)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), flag + "=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e = cmd.Output()
	if e != nil {
		t.Fatalf("actual Source: %v\n%s", e, stderr.String())
	}
	if e = json.Unmarshal(data, output); e != nil {
		t.Fatalf("actual Source decode: %v\n%s\n%s", e, data, stderr.String())
	}
}

func qqEncryptFixture(t *testing.T, key, nonce, plain []byte) string {
	t.Helper()
	b, e := aes.NewCipher(key)
	if e != nil {
		t.Fatal(e)
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		t.Fatal(e)
	}
	return base64.StdEncoding.EncodeToString(append(append([]byte{}, nonce...), g.Seal(nil, nonce, plain, nil)...))
}

func TestQQActualVendorEncodingAndSecretProtocol(t *testing.T) {
	urls := []map[string]json.RawMessage{}
	for _, task := range []string{`"private-task"`, `"a b!*'()~/_+-中国😀"`, `17`, `null`, `false`, `["a","b"]`, `{}`, `"\ud800"`, `"\udfff"`} {
		for _, source := range []string{`"claude-code-haha"`, `"a b!*中国😀"`, `""`, `"\ud800"`, `null`, `false`, `17`, `[]`, `{}`} {
			urls = append(urls, map[string]json.RawMessage{"task": json.RawMessage(task), "source": json.RawMessage(source)})
		}
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	key64 := base64.StdEncoding.EncodeToString(key)
	decrypt := []map[string]string{}
	for _, n := range []int{0, 1, 4, 5, 8, 11, 12, 13, 14, 15, 16, 20, 27, 28, 40} {
		v := make([]byte, n)
		for i := range v {
			v[i] = byte(i)
		}
		for _, k := range []string{key64, "", "bad", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
			decrypt = append(decrypt, map[string]string{"encrypted": base64.StdEncoding.EncodeToString(v), "key": k})
		}
	}
	for _, plain := range [][]byte{nil, []byte("private-secret"), []byte("中文😀"), {0xff, 0xfe, 'A', 0xe0, 0xa0}} {
		value := qqEncryptFixture(t, key, make([]byte, 12), plain)
		decrypt = append(decrypt, map[string]string{"encrypted": value, "key": key64}, map[string]string{"encrypted": " \n" + value + "?!", "key": key64[:len(key64)-1]})
	}
	var source struct{ URLs, Decrypt []map[string]any }
	runQQSource(t, "native-qq-protocol.fixture.ts", "CC_HAHA_NATIVE_QQ_PROTOCOL_FIXTURE", map[string]any{"urls": urls, "decrypt": decrypt}, &source)
	check := func(name string, at int, value string, e error, want map[string]any) {
		got := map[string]any{"value": value}
		expected := map[string]any{"value": want["value"]}
		if e != nil {
			got = map[string]any{"error": e.Error()}
		}
		if err, ok := want["error"]; ok {
			expected = map[string]any{"error": err}
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s[%d] Go%v Source%v", name, at, got, expected)
		}
	}
	for i, row := range urls {
		v, e := QQConnectURL(row["task"], row["source"])
		check("URL", i, v, e, source.URLs[i])
	}
	for i, row := range decrypt {
		v, e := QQDecryptSecret(row["encrypted"], row["key"])
		check("decrypt", i, v, e, source.Decrypt[i])
	}
	t.Log(fmt.Sprintf("%d actual vendor encodeURIComponent/decrypt cases", len(urls)+len(decrypt)))
}

func TestQQActualVendorRawBufferSecrets(t *testing.T) {
	key := make([]byte, 32)
	encrypted := qqEncryptFixture(t, key, make([]byte, 12), []byte("private-array-secret"))
	value, e := base64.StdEncoding.DecodeString(encrypted)
	if e != nil {
		t.Fatal(e)
	}
	array := make([]any, len(value))
	arrayLike := map[string]any{"length": len(value)}
	for i, b := range value {
		array[i] = int(b)
		arrayLike[fmt.Sprint(i)] = int(b)
	}
	rows := []map[string]any{}
	for _, raw := range []any{array, map[string]any{"type": "Buffer", "data": array}, arrayLike} {
		rows = append(rows, map[string]any{"encrypted": raw, "key": base64.StdEncoding.EncodeToString(key)})
	}
	var source struct{ Decrypt []map[string]any }
	runQQSource(t, "native-qq-protocol.fixture.ts", "CC_HAHA_NATIVE_QQ_PROTOCOL_FIXTURE", map[string]any{"urls": []any{}, "decrypt": rows}, &source)
	for i, row := range rows {
		raw, _ := json.Marshal(row["encrypted"])
		got, e := QQDecryptSecretJSON(raw, row["key"].(string))
		if e != nil || got != source.Decrypt[i]["value"] {
			t.Fatalf("actual Buffer form%d Go%q %v Source%v", i, got, e, source.Decrypt[i])
		}
	}
}
