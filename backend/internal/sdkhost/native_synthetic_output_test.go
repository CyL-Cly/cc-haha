package sdkhost

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestSyntheticOutputHasActualOwningHandlerAndDrainsAsyncReference(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	var references ownerasync.NativeReferences
	native, e := references.Bind(owner)
	if e != nil {
		t.Fatal(e)
	}
	called := 0
	validate := func(context.Context, protocol.JSONObject) error { called++; return nil }
	if _, e = NewNativeSyntheticOutput(foreign, native, []byte(`{"type":"object"}`), validate); !errors.Is(e, cooperative.ErrRestricted) && e == nil {
		t.Fatal("foreign native resource accepted")
	}
	binding, e := NewNativeSyntheticOutput(owner, native, []byte(`{"type":"object"}`), validate)
	if e != nil {
		t.Fatal(e)
	}
	if e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error { _, _, e := binding.catalog(scope); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	var tool queryloop.RegisteredTool
	if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		definition, actual, e := binding.catalog(scope)
		if e != nil {
			return e
		}
		tool = actual
		if definition.Name != "StructuredOutput" || !binding.allows(owner, actual) || binding.allows(foreign, actual) || actual.StartCallJSON == nil || !actual.IsConcurrencySafe(protocol.NewJSONObject()) {
			return errors.New("synthetic ownership or actual async handler missing")
		}
		future, e := actual.StartCallJSON(context.Background(), protocol.NewJSONObject(protocol.JSONField{Name: "answer", Value: quote("actual")}), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
		if e != nil {
			return e
		}
		result, e := future.Await(scope)
		if e != nil {
			return e
		}
		if string(result.StructuredOutput) != `{"answer":"actual"}` {
			return errors.New("actual async result lost input")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if called != 1 {
		t.Fatal("validation bypassed", called)
	}
	if e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := tool.StartCallJSON(context.Background(), protocol.NewJSONObject(), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
		return e
	}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e = tool.StartCallJSON(context.Background(), protocol.NewJSONObject(), queryloop.JSONToolCallContext{}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error { return scope.AwaitFulfilled() }); e != nil {
		t.Fatal(e)
	}
	if count, _ := references.Snapshot(); count != 0 || called != 1 {
		t.Fatal("foreign calls or actual resource leaked", count, called)
	}
}
