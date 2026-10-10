package builtinwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type observedReadFiles struct {
	OSFiles
	events                []string
	sampleN               int
	sampleError           error
	zeroBefore            bool
	position              int64
	readFault, closeFault error
	fullReads             int
}
type observedHandle struct {
	ReadHandle
	parent *observedReadFiles
}

func (f *observedReadFiles) OpenRead(p string) (ReadHandle, error) {
	f.events = append(f.events, "open "+p)
	h, e := f.OSFiles.OpenRead(p)
	if e != nil {
		return h, e
	}
	return &observedHandle{h, f}, nil
}
func (h *observedHandle) Fd() uintptr {
	h.parent.events = append(h.parent.events, "Fd")
	return h.ReadHandle.Fd()
}
func (h *observedHandle) ReadAt(buffer []byte, pos int64) (int, error) {
	f := h.parent
	f.zeroBefore = reflect.DeepEqual(buffer, make([]byte, 4096))
	f.position = pos
	f.events = append(f.events, "ReadAt")
	n, e := h.ReadHandle.ReadAt(buffer, pos)
	f.sampleN = n
	f.sampleError = e
	if f.readFault != nil {
		return n, f.readFault
	}
	return n, e
}
func (h *observedHandle) Close() error {
	f := h.parent
	f.events = append(f.events, "Close")
	e := h.ReadHandle.Close()
	if e != nil {
		return e
	}
	return f.closeFault
}
func (f *observedReadFiles) ReadFile(p string) ([]byte, error) {
	f.events = append(f.events, "fullRead "+p)
	f.fullReads++
	return f.OSFiles.ReadFile(p)
}
func TestWriteNativeMetadataEncodingBOMCRLFAndRawEOF(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name     string
		data     []byte
		encoding string
		want     []uint16
	}{{"utf8", []byte("a\r\nb\r\n"), "utf8", []uint16{'a', 10, 'b', 10}}, {"utf8bom", []byte{0xef, 0xbb, 0xbf, 'x', '\r', '\n'}, "utf8", []uint16{0xfeff, 'x', 10}}, {"utf16", []byte{0xff, 0xfe, 'a', 0, 13, 0, 10, 0, 0, 0xd8, 0x9a}, "utf16le", []uint16{0xfeff, 'a', 10, 0xd800}}, {"empty", nil, "utf8", []uint16{}}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(home, c.name)
			if e := os.WriteFile(path, c.data, 0600); e != nil {
				t.Fatal(e)
			}
			f := &observedReadFiles{}
			meta, e := readMetadata(f, path)
			if e != nil || meta.encoding != c.encoding || !reflect.DeepEqual(meta.content, c.want) {
				t.Fatalf("metadata %+v %v want%v", meta, e, c.want)
			}
			if f.sampleN != len(c.data) || f.sampleError != io.EOF || !f.zeroBefore || f.position != 0 || f.fullReads != 1 || len(f.events) != 5 || f.events[3] != "Close" {
				t.Fatalf("actual native receipt %+v", f)
			}
			t.Logf("actual Go ReadAt n=%d err=%v position=%d zeroBefore=%v order=%v; Source short-read success adapted explicitly", f.sampleN, f.sampleError, f.position, f.zeroBefore, f.events)
		})
	}
}
func TestWriteNativeMetadataReadCloseFailurePriorityAndNoFullRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if e := os.WriteFile(path, []byte("bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	read := errors.New("read result fault after native positioned read")
	close := errors.New("close result fault after native descriptor close")
	f := &observedReadFiles{readFault: read, closeFault: close}
	_, e := readMetadata(f, path)
	if e != close || f.fullReads != 0 || !f.zeroBefore || f.events[len(f.events)-1] != "Close" {
		t.Fatalf("finally %v %+v", e, f)
	}
}
func TestWriteNativeUTF8InvalidExistingFileExplicitGuard(t *testing.T) {
	s, o, _, f, home := fixture(t)
	path := filepath.Join(home, "invalid")
	if e := os.WriteFile(path, []byte{0xfe, 0xff}, 0600); e != nil {
		t.Fatal(e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		_, e := s.Call(context.Background(), input(path, "x"), scope)
		return expectCapability(e, "invalid UTF8")
	})
	b, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(b, []byte{0xfe, 0xff}) || len(f.events) != 0 {
		t.Fatalf("invalid metadata wrote %v %v %v", b, e, f.events)
	}
}
func TestWriteNewSurrogatesNULAndExplicitLineEndingsInActualBytes(t *testing.T) {
	s, o, _, _, home := fixture(t)
	in := input("unicode", "")
	in.Set("content", json.RawMessage(`"\ud800\u0000a\r\nb\n"`))
	run(t, o, func(scope *cooperative.Scope) error {
		data, e := s.Call(context.Background(), in, scope)
		if e != nil {
			return e
		}
		var actual protocol.JSONObject
		if e = json.Unmarshal(data, &actual); e != nil {
			return e
		}
		units, e := protocol.DecodeJavaScriptJSONString(actual.Get("content"))
		want, _ := protocol.DecodeJavaScriptJSONString(in.Get("content"))
		if e != nil || !reflect.DeepEqual(units, want) {
			return fmt.Errorf("raw UTF16 result %v %v", units, e)
		}
		added, removed, e := s.LineChanges(scope)
		if e != nil || added != 3 || removed != 0 {
			return fmt.Errorf("real counters %d/%d %v", added, removed, e)
		}
		return nil
	})
	b, e := os.ReadFile(filepath.Join(home, "unicode"))
	want := []byte("�\x00a\r\nb\n")
	if e != nil || !bytes.Equal(b, want) {
		t.Fatalf("native UTF8 bytes %x want%x %v", b, want, e)
	}
}
func TestWriteSharedAtomicEngineUTF16LEPreservesUnitsAndMode(t *testing.T) {
	s, o, _, _, home := fixture(t)
	path := filepath.Join(home, "utf16-engine")
	if e := os.WriteFile(path, []byte{0xff, 0xfe}, 0640); e != nil {
		t.Fatal(e)
	}
	units := []uint16{0xfeff, 'x', 13, 10, 0xd800}
	run(t, o, func(scope *cooperative.Scope) error { return s.atomicWrite(path, contentBytes(units, "utf16le")) })
	b, e := os.ReadFile(path)
	info, stat := os.Stat(path)
	if e != nil || stat != nil || !bytes.Equal(b, []byte{0xff, 0xfe, 'x', 0, 13, 0, 10, 0, 0, 0xd8}) || info.Mode().Perm() != 0640 {
		t.Fatalf("actual native UTF16 %x %v mode%v %v", b, e, info, stat)
	}
}
