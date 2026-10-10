package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func originalChild(t *testing.T, m protocol.JSONObject, key string) protocol.JSONObject {
	t.Helper()
	a, ok := any(m).(interface {
		Object(string) (protocol.JSONObject, bool)
	})
	if !ok {
		t.Fatal("raw ingress lacks actual shared nested representation")
	}
	child, ok := a.Object(key)
	if !ok {
		t.Fatal(key)
	}
	return child
}
func TestRawRecordOriginalFilteredBlockReferenceAcrossStorage(t *testing.T) {
	physical := &uuidIO{kind: "readFile", entered: make(chan struct{}), released: make(chan struct{})}
	owner, w, p, loader := recordHost(t, OSFiles{}, physical)
	if e := os.MkdirAll(filepath.Dir(w.target()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(w.target(), []byte(uuidOld), 0600); e != nil {
		t.Fatal(e)
	}
	var release sync.Once
	unblock := func() { release.Do(func() { close(physical.released) }) }
	t.Cleanup(unblock)
	m := toolRecordObject(t, `{"type":"assistant","uuid":"raw","isVirtual":true,"unknown":{"keep":1},"message":{"role":"assistant","stop_reason":null,"content":[{"type":"text","text":"before","unknown":{"nested":true}},{"type":"tool_use","id":"r","name":"REPL","input":{}}]}}`)
	future := startRecord(t, owner, p, loader, []LiveRecord{{Record: m}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
	awaitSignal(t, physical.entered, "actual native UUID cache read")
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		api, ok := m.Object("message")
		if !ok {
			return fmt.Errorf("original API object absent")
		}
		api.Set("stop_reason", quote("end_turn"))
		blocks, ok := api.Array("content")
		if !ok {
			return fmt.Errorf("original block array absent")
		}
		block, ok := blocks.Object(0)
		if !ok {
			return fmt.Errorf("original kept block absent")
		}
		block.Set("text", quote("before later"))
		m.Set("lateOuter", quote("new original outer"))
		return nil
	})
	unblock()
	if _, e := future.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := flushRecord(t, owner, p).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	data := recordBytes(t, w)
	if !strings.Contains(data, `"text":"before later"`) || !strings.Contains(data, `"stop_reason":null`) || !strings.Contains(data, `"unknown":{"keep":1}`) || strings.Contains(data, `"lateOuter"`) || strings.Contains(data, `"name":"REPL"`) {
		t.Fatal(data)
	}
}
func TestRawRecordNullBlockRejectsOriginalPromiseWithFreshTypeError(t *testing.T) {
	owner, w, p, loader := recordHost(t, OSFiles{}, nil)
	m := toolRecordObject(t, `{"type":"user","uuid":"null","message":{"role":"user","content":[null]}}`)
	var futures [2]*RecordFuture
	for i := range futures {
		pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
			var e error
			futures[i], e = p.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: m}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
			if e != nil || futures[i] == nil {
				return fmt.Errorf("async Source record clean throw escaped as synchronous admission: %v", e)
			}
			return nil
		})
	}
	_, first := futures[0].Wait(context.Background())
	_, second := futures[1].Wait(context.Background())
	for _, e := range []error{first, second} {
		named, ok := e.(interface{ ErrorName() string })
		if !ok || named.ErrorName() != "TypeError" || e.Error() != "null is not an object (evaluating 'b.type')" {
			t.Fatalf("original native null error contract: %T %v", e, e)
		}
		var cap *CapabilityError
		if errors.As(e, &cap) {
			t.Fatal("native TypeError relabeled as capability")
		}
	}
	if first == second {
		t.Fatal("different actual null property accesses reused one error identity")
	}
	_, again := futures[0].Wait(context.Background())
	if again != first {
		t.Fatal("Record rejected promise copied original error object")
	}
	_ = w
}

func TestRawRecordPrimitiveBlocksAreRetainedAndUndefinedAccessThrowsTypeError(t *testing.T) {
	m := toolRecordObject(t, `{"type":"user","uuid":"primitive","message":{"role":"user","content":[0,false,"text"]}}`)
	cleaned, e := cleanRecord([]LiveRecord{{Record: m}})
	if e != nil || len(cleaned) != 1 {
		t.Fatal(e)
	}
	api, _ := m.Object("message")
	content, _ := api.Array("content")
	if e := content.Append(nil); e != nil {
		t.Fatal(e)
	}
	_, e = cleanRecord([]LiveRecord{{Record: m}})
	native, ok := e.(*LoggingTypeError)
	if !ok || native.ErrorName() != "TypeError" || native.Error() != "undefined is not an object (evaluating 'b.type')" {
		t.Fatalf("native explicit undefined access: %T %v", e, e)
	}
}
