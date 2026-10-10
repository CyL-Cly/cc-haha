package providerproxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestOpenAICompressionActualSourceAndCrossRuntimeDecode(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type profile struct {
		Plain          string            `json:"plain"`
		OptOut         string            `json:"optOut"`
		Headers        map[string]string `json:"headers"`
		Unsupported    bool              `json:"unsupported"`
		EncoderFailure bool              `json:"encoderFailure"`
		AbortBefore    bool              `json:"abortBefore"`
		AbortAfter     bool              `json:"abortAfter"`
		ForeignWire    string            `json:"foreignWire,omitempty"`
	}
	rows := []profile{}
	for _, plain := range []string{"", `{"model":"gpt-6-sol","stream":true}`, "\ufeffUnicode 😀文本\n", strings.Repeat(`{"Read":"same-private-history"}`, 20000)} {
		for _, optOut := range []string{"", "0", "false", "FALSE", "off", "Off", " false ", "1", "true"} {
			rows = append(rows, profile{Plain: plain, OptOut: optOut, Headers: map[string]string{"content-type": "application/json", "content-length": "original-length"}})
		}
	}
	for _, encoding := range []string{"", "identity", "gzip", "zstd"} {
		rows = append(rows, profile{Plain: "original JSON bytes", Headers: map[string]string{"content-encoding": encoding, "content-length": "99"}})
	}
	for _, flags := range []profile{{Unsupported: true}, {EncoderFailure: true}, {AbortBefore: true}, {AbortAfter: true}, {EncoderFailure: true, AbortAfter: true}, {AbortBefore: true, Unsupported: true}, {AbortAfter: true, OptOut: "off"}} {
		flags.Plain = `{"messages":[{"role":"user","content":"private fixture"}]}`
		flags.Headers = map[string]string{"content-length": "99"}
		rows = append(rows, flags)
	}
	observed := []json.RawMessage{}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	for index, row := range rows {
		ctx, cancel := context.WithCancelCause(context.Background())
		reason := errors.New("fixture-abort-reason")
		if row.AbortBefore {
			cancel(reason)
		}
		calls := []any{}
		encoder := OpenAIEncode(func(plain []byte) ([]byte, error) {
			calls = append(calls, map[string]any{"level": 3})
			if row.AbortAfter {
				cancel(reason)
			}
			if row.EncoderFailure {
				return nil, errors.New("fixture-local-encoder-failure")
			}
			return EncodeOpenAIZstd(plain)
		})
		if row.Unsupported {
			encoder = nil
		}
		headers := requestRecord()
		keys := []string{}
		for key := range row.Headers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			headers.Set(key, requestEncode(row.Headers[key]))
		}
		encoded, err := EncodeOpenAIRequestBody(ctx, []byte(row.Plain), &headers, row.OptOut, encoder)
		cancel(nil)
		if err != nil {
			observed = append(observed, requestEncode(requestRecord(requestField("error", err.Error()), requestField("calls", calls))))
			continue
		}
		kind, decoded := "plain", string(encoded.Wire)
		var audit any
		if encoded.Encoded {
			kind = "encoded"
			plain, failure := decoder.DecodeAll(encoded.Wire, nil)
			if failure != nil || string(plain) != row.Plain {
				t.Fatalf("row%d actual Go zstd roundtrip %v %q", index, failure, plain)
			}
			decoded = string(plain)
			audit = requestRecord(requestField("plainBody", row.Plain), requestField("requestEncoding", encoded.Encoding), requestField("requestPlainBytes", encoded.PlainBytes))
			rows[index].ForeignWire = base64.StdEncoding.EncodeToString(encoded.Wire)
			if len(encoded.Wire) != encoded.WireBytes {
				t.Fatal("actual audit did not retain the submitted wire length")
			}
		}
		sortedHeaders := requestRecord()
		keys = keys[:0]
		for _, field := range headers.OwnProperties() {
			var key string
			_ = json.Unmarshal(field.NameJSON, &key)
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			sortedHeaders.Set(key, headers.Get(key))
		}
		var foreign any
		if encoded.Encoded {
			foreign = row.Plain
		}
		observed = append(observed, requestEncode(requestRecord(requestField("kind", kind), requestField("decoded", decoded), requestField("headers", sortedHeaders), requestField("calls", calls), requestField("audit", audit), requestField("foreignDecoded", foreign))))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-compression.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_COMPRESSION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(requestEncode(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source compression %v %s", err, stderr.String())
	}
	want := requestArray(output)
	if len(want) != len(rows) {
		t.Fatalf("actual Source compression row count%d expected%d", len(want), len(rows))
	}
	for i := range observed {
		if !bytes.Equal(observed[i], requestEncode(want[i])) {
			t.Errorf("row%d actual %s want%s", i, observed[i], requestEncode(want[i]))
		}
	}
	t.Logf("compared %d actual Source compression/header/audit/abort profiles; actual Bun decoded Go frames", len(rows))
}
