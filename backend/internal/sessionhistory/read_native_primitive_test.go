package sessionhistory

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestReadNativePrimitiveKeepsOriginalRequestJSONAndBothLargeFilePasses(t *testing.T) {
	large := `{"type":"custom-title","sessionId":"s","customTitle":"before"}` + "\n" +
		row(nativeUUID, "null", "user", `,"huge":"`+strings.Repeat("x", 6*1024*1024)+`"`) +
		`{"type":"system","subtype":"compact_boundary","uuid":"boundary"}` + "\n" +
		row("last", `"boundary"`, "user", "")
	for _, sample := range []struct {
		name, content string
		disabled      bool
		stages        []ReadPrimitive
	}{
		{"small", row(nativeUUID, "null", "user", ""), false, []ReadPrimitive{ReadPrimitiveFSFunction, ReadPrimitiveFSFunction}},
		{"disabled-large", large, true, []ReadPrimitive{ReadPrimitiveFSFunction}},
		{"large-with-metadata", large, false, []ReadPrimitive{ReadPrimitiveFSFunction, ReadPrimitiveFSFunction, ReadPrimitiveFileHandle, ReadPrimitiveFileHandleClose, ReadPrimitiveStream, ReadPrimitiveStream, ReadPrimitiveStream}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			loader, _ := nativeLoader(t)
			loader.options.DisablePrecompactSkip = func() bool { return sample.disabled }
			path := writeHistory(t, sample.content)
			expected, err := loader.LoadTranscriptFile(path, false)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := loader.BeginRead(path, false)
			if err != nil {
				t.Fatal(err)
			}
			var handle interface {
				ReadAt([]byte, int64) (int, error)
				Close() error
			}
			defer func() {
				if handle != nil {
					_ = handle.Close()
				}
			}()
			var stages []ReadPrimitive
			lastOperation := ReadComplete
			for reader.Request().Operation != ReadComplete {
				request := reader.Request()
				primitive := reader.NativePrimitive()
				if request != reader.Request() || primitive != reader.NativePrimitive() {
					t.Fatal("readonly primitive query advanced the read")
				}
				encoded, _ := json.Marshal(request)
				legacy, _ := json.Marshal(struct {
					Operation ReadOperation
					Path      string
					Offset    int64
					Length    int
				}{request.Operation, request.Path, request.Offset, request.Length})
				if !bytes.Equal(encoded, legacy) {
					t.Fatal("old ReadRequest JSON changed", string(encoded), string(legacy))
				}
				if request.Operation != lastOperation {
					stages = append(stages, primitive)
					lastOperation = request.Operation
				}
				var result PhysicalReadResult
				switch request.Operation {
				case ReadStat:
					result.Size, result.Error = loader.options.Stat(request.Path)
				case ReadWhole:
					result.Bytes, result.Error = loader.options.ReadFile(request.Path)
				case ReadOpen:
					handle, result.Error = loader.options.Files.Open(request.Path)
				case ReadAt:
					chunk := make([]byte, request.Length)
					n, err := handle.ReadAt(chunk, request.Offset)
					result.Bytes, result.Error = chunk[:n], err
					if err != nil && err != io.EOF {
						t.Fatal(err)
					}
				case ReadClose:
					result.Error = handle.Close()
					handle = nil
				default:
					t.Fatal("foreign original read operation", request.Operation)
				}
				if err = reader.Complete(result); err != nil {
					t.Fatal(err)
				}
			}
			if reader.NativePrimitive() != ReadPrimitiveNone || !reflect.DeepEqual(stages, sample.stages) {
				t.Fatal("actual filesystem/FileHandle/metadata-stream route changed", stages, sample.stages)
			}
			actual, err := loader.FinishRead(reader)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(expected)
			if !bytes.Equal(a, b) {
				t.Fatal("sampling primitive changed original parser/metadata result")
			}
		})
	}
	var absent *ReadContinuation
	if absent.NativePrimitive() != ReadPrimitiveNone {
		t.Fatal("nil read has a native primitive")
	}
}
