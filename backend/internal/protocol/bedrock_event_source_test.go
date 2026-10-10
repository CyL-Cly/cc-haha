package protocol

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func privateEventFrame(kind, data string) []byte {
	rows := [][2]string{{":message-type", "event"}, {":event-type", kind}, {":content-type", "application/json"}}
	headers := []byte{}
	for _, row := range rows {
		headers = append(headers, byte(len(row[0])))
		headers = append(headers, row[0]...)
		headers = append(headers, 7, byte(len(row[1])>>8), byte(len(row[1])))
		headers = append(headers, row[1]...)
	}
	length := len(headers) + len(data) + 16
	value := make([]byte, length)
	binary.BigEndian.PutUint32(value, uint32(length))
	binary.BigEndian.PutUint32(value[4:], uint32(len(headers)))
	binary.BigEndian.PutUint32(value[8:], crc32.ChecksumIEEE(value[:8]))
	copy(value[12:], headers)
	copy(value[12+len(headers):], data)
	binary.BigEndian.PutUint32(value[length-4:], crc32.ChecksumIEEE(value[:length-4]))
	return value
}

func TestActualSmithyEventStreamCRCAndOriginalChunkedStreamTruncation(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	wire := privateEventFrame("chunk", `{"bytes":"`+base64.StdEncoding.EncodeToString([]byte(`{"type":"future","unknown":"🙂"}`))+`"}`)
	corruptPrelude := bytes.Clone(wire)
	corruptPrelude[8] ^= 1
	corruptBody := bytes.Clone(wire)
	corruptBody[len(wire)-1] ^= 1
	values := [][]byte{wire, corruptPrelude, corruptBody, wire[:3], wire[:8], wire[:len(wire)-1]}
	rows := []map[string]any{}
	for _, value := range values {
		rows = append(rows, map[string]any{"kind": "event", "hex": hex.EncodeToString(value)})
	}
	for _, step := range []int{1, 2, 3, 4, 7, 31, len(wire)} {
		chunks := []string{}
		for start := 0; start < len(wire); start += step {
			chunks = append(chunks, hex.EncodeToString(wire[start:min(start+step, len(wire))]))
		}
		rows = append(rows, map[string]any{"kind": "eventChunks", "chunks": chunks})
	}
	for _, value := range [][]byte{wire[:3], wire[:8], wire[:len(wire)-1]} {
		rows = append(rows, map[string]any{"kind": "eventChunks", "chunks": []string{hex.EncodeToString(value)}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-primitives.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AWS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []map[string]json.RawMessage }
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err)
	}
	for index, value := range values {
		_, _, err := DecodeBedrockMessage(value)
		if err != nil {
			var expected string
			if json.Unmarshal(source.Rows[index]["error"], &expected) != nil {
				t.Fatalf("Source case%d unexpectedly passed", index)
			}
			if err.Error() != expected {
				t.Errorf("case%d Go%q Source%q", index, err.Error(), expected)
			}
		} else {
			if source.Rows[index]["error"] != nil {
				t.Fatal("Source rejected valid event", string(source.Rows[index]["error"]))
			}
		}
	}
	for index, row := range rows[len(values):] {
		raw := []byte{}
		for _, chunk := range row["chunks"].([]string) {
			value, _ := hex.DecodeString(chunk)
			raw = append(raw, value...)
		}
		decoder := NewBedrockEventDecoder(nil)
		frames := []SDKSSEEvent{}
		var failure error
		for _, chunk := range row["chunks"].([]string) {
			value, _ := hex.DecodeString(chunk)
			decoder.AcceptRead(value, nil)
			for {
				next, ready, err := decoder.PollChunk()
				if err != nil {
					failure = err
					break
				}
				if !ready {
					break
				}
				decoder.DecodeChunk(next)
				if frame, ok := decoder.PollFrame(); ok {
					frames = append(frames, frame)
				}
			}
		}
		decoder.AcceptRead(nil, io.EOF)
		for failure == nil {
			next, ready, err := decoder.PollChunk()
			if err != nil {
				if err != io.EOF {
					failure = err
				}
				break
			}
			if !ready {
				break
			}
			decoder.DecodeChunk(next)
			if frame, ok := decoder.PollFrame(); ok {
				frames = append(frames, frame)
			}
		}
		var expected struct {
			Messages []string
			Error    *string
		}
		joined, _ := json.Marshal(source.Rows[index+len(values)])
		if err = json.Unmarshal(joined, &expected); err != nil {
			t.Fatal(err)
		}
		var errorText *string
		if failure != nil {
			value := failure.Error()
			errorText = &value
		}
		if !reflect.DeepEqual(errorText, expected.Error) {
			t.Errorf("chunk%d Go%v Source%v", index, errorText, expected.Error)
		}
		if len(frames) != len(expected.Messages) {
			t.Errorf("chunk%d framecount Go%d Source%d", index, len(frames), len(expected.Messages))
		}
		if len(frames) > 0 && frames[0].Data != `{"type":"future","unknown":"🙂"}` {
			t.Fatal("chunk JSON changed", frames[0])
		}
		_ = raw
	}
}
