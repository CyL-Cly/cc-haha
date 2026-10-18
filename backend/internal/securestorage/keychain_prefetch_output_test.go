package securestorage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestKeychainPrefetchOutputStreamingUTF8MatchesWholeDecodeAtEverySplit(t *testing.T) {
	for _, input := range [][]byte{
		[]byte("ascii 中文 é 😀 \ufeff"),
		{0xe2, 0x82}, {0xc2}, {0xf0, 0x9f, 0x98},
		{0xe2, 0x82, 'x', 0xff, 0xc2, 'y'},
		{0xe0, 0x80, 0x80, 0xed, 0xa0, 0x80},
		{0xf4, 0x90, 0x80, 0x80, 0xf4, 0x8f, 0xbf, 0xbf},
	} {
		before := bytes.Clone(input)
		for split := 0; split <= len(input); split++ {
			kills := 0
			output := &keychainPrefetchOutput{kill: func() error { kills++; return nil }}
			for _, chunk := range [][]byte{input[:split], input[split:]} {
				if n, err := output.Write(chunk); n != len(chunk) || err != nil {
					t.Fatal("stream write", n, err)
				}
			}
			output.finish()
			if kills != 0 || output.overflow || !bytes.Equal(output.data.Bytes(), protocol.DecodeJavaScriptUTF8(input)) || !bytes.Equal(input, before) {
				t.Fatalf("UTF-8 split%d input%x got%x", split, input, output.data.Bytes())
			}
		}
		output := &keychainPrefetchOutput{kill: func() error { t.Error("small byte stream overflowed"); return nil }}
		for _, value := range input {
			if _, err := output.Write([]byte{value}); err != nil {
				t.Fatal(err)
			}
		}
		output.finish()
		if !bytes.Equal(output.data.Bytes(), protocol.DecodeJavaScriptUTF8(input)) {
			t.Fatalf("single-byte stream %x", input)
		}
	}
}

func TestKeychainPrefetchOutputBuffersMalformedIncompleteShapeLikePinnedStringDecoder(t *testing.T) {
	for _, pending := range [][]byte{{0xc0}, {0xe0, 0x80}, {0xed, 0xa0}, {0xf4, 0x90}} {
		for _, endWithEOF := range []bool{false, true} {
			output := &keychainPrefetchOutput{kill: func() error { t.Error("small decoder prefix overflowed"); return nil }}
			for _, value := range pending {
				if _, err := output.Write([]byte{value}); err != nil {
					t.Fatal(err)
				}
			}
			if output.data.Len() != 0 {
				t.Fatal("malformed incomplete suffix emitted before original decoder boundary", pending, output.data.Bytes())
			}
			want := protocol.DecodeJavaScriptUTF8(pending)
			if endWithEOF {
				output.finish()
			} else {
				if _, err := output.Write([]byte{'x'}); err != nil {
					t.Fatal(err)
				}
				want = append(want, 'x')
			}
			if !bytes.Equal(output.data.Bytes(), want) {
				t.Fatal("saved malformed prefix lost accepted bytes", pending, output.data.Bytes(), want)
			}
		}
	}
	kills := 0
	output := &keychainPrefetchOutput{kill: func() error { kills++; return nil }}
	if _, err := output.Write(bytes.Repeat([]byte{'a'}, keychainPrefetchMaxBuffer-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte{0xc0}); err != nil {
		t.Fatal(err)
	}
	if output.overflow || kills != 0 || output.data.Len() != keychainPrefetchMaxBuffer-1 {
		t.Fatal("invalid pending shape crossed maxBuffer before Source emitted it")
	}
	output.finish()
	if !output.overflow || kills != 1 {
		t.Fatal("decoder EOF did not count replacement expansion")
	}
}

func TestKeychainPrefetchOutputDecodedMaxBufferRejectsInvalidUTF8AcrossChunks(t *testing.T) {
	for _, invalid := range [][]byte{{0xff}, {0xe2, 0x82}} {
		input := append([]byte{'"'}, bytes.Repeat(invalid, 350000)...)
		input = append(input, '"')
		if len(input) >= keychainPrefetchMaxBuffer || len(protocol.DecodeJavaScriptUTF8(input)) <= keychainPrefetchMaxBuffer {
			t.Fatal("invalid-domain fixture does not cross only the decoded limit")
		}
		for _, width := range []int{len(input), 17, 4093} {
			kills := 0
			output := &keychainPrefetchOutput{kill: func() error { kills++; return nil }}
			for start := 0; start < len(input); start += width {
				end := start + width
				if end > len(input) {
					end = len(input)
				}
				if _, err := output.Write(input[start:end]); err != nil {
					t.Fatal(err)
				}
			}
			output.finish()
			if kills != 1 || !output.overflow || !utf8.Valid(output.data.Bytes()) {
				t.Fatal("raw-byte limit accepted decoded overflow", invalid, width, kills, output.data.Len())
			}
			// Actual pinned Bun's single decoded chunk uses a UTF-16 slice
			// count larger than this fixture's code-unit length, retaining all
			// 1,050,002 UTF-8 bytes on the failed callback.
			if width == len(input) && !bytes.Equal(output.data.Bytes(), protocol.DecodeJavaScriptUTF8(input)) {
				t.Fatal("execFile UTF-16 truncation was replaced by a raw-byte cap")
			}
		}
	}
}

func TestKeychainPrefetchOutputValidMultibyteBoundaryAndEOFReplacementLimit(t *testing.T) {
	for _, character := range []string{"é", "€", "😀"} {
		kills := 0
		output := &keychainPrefetchOutput{kill: func() error { kills++; return nil }}
		prefix := bytes.Repeat([]byte{'a'}, keychainPrefetchMaxBuffer-len(character))
		if _, err := output.Write(prefix); err != nil {
			t.Fatal(err)
		}
		for _, value := range []byte(character) {
			if _, err := output.Write([]byte{value}); err != nil {
				t.Fatal(err)
			}
		}
		output.finish()
		if kills != 0 || output.overflow || !bytes.Equal(output.data.Bytes(), append(prefix, []byte(character)...)) {
			t.Fatal("valid split multibyte sequence was counted as replacements", character, kills, output.data.Len())
		}
	}
	kills := 0
	output := &keychainPrefetchOutput{kill: func() error { kills++; return nil }}
	if _, err := output.Write(bytes.Repeat([]byte{'a'}, keychainPrefetchMaxBuffer-2)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []byte{0xe2, 0x82} {
		if _, err := output.Write([]byte{value}); err != nil {
			t.Fatal(err)
		}
	}
	if output.overflow || kills != 0 || output.data.Len() != keychainPrefetchMaxBuffer-2 {
		t.Fatal("incomplete prefix emitted before EOF")
	}
	output.finish()
	output.finish()
	if !output.overflow || kills != 1 || !bytes.HasSuffix(output.data.Bytes(), []byte("\ufffd")) {
		t.Fatal("actual EOF replacement bypassed decoded maxBuffer", kills, output.data.Len())
	}
}

func TestKeychainPrefetchActualInvalidUTF8DecodedOverflowPrimesEmptyCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual POSIX security byte-output primitive")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte{'"'}, bytes.Repeat([]byte{0xff}, 350000)...)
	payload = append(payload, '"')
	if err := os.WriteFile(filepath.Join(home, "owned-payload"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	program := "#!/bin/sh\ncase \"$6\" in *-credentials*) exec /bin/cat \"$HOME/owned-payload\";; *) exit 44;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "darwin", Environment: map[string]string{"HOME": home, "USER": "owned-invalid-utf8", "PATH": bin + ":/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefetch, err := store.NewKeychainPrefetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer prefetch.Close()
	if err := prefetch.RunOAuth(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.RunLegacy(); err != nil {
		t.Fatal(err)
	}
	if !prefetch.arms[0].process.stdout.overflow || prefetch.arms[0].timedOut {
		t.Fatal("decoded maxBuffer did not retain Source RangeError without killed")
	}
	if err := prefetch.Publish(); err != nil {
		t.Fatal(err)
	}
	if store.cachedAt.IsZero() || store.cachedJSON != nil {
		t.Fatal("invalid-byte successful JSON shadowed Source's completed empty cache")
	}
	var reads atomic.Int32
	store.options.RunSecurity = func(context.Context, Command) ([]byte, error) { reads.Add(1); return nil, nil }
	if data, err := store.ReadSourceJSON(ctx); err != nil || string(data) != "{}" || reads.Load() != 0 {
		t.Fatal("actual shared empty cache was not consumed", string(data), reads.Load(), err)
	}
}
