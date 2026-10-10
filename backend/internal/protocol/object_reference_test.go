package protocol_test

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Frozen actual Source handleStopHooks/cache-safe reference probe observations.
// Late mutations are explicit primitive operations, not production producers.
//
//go:embed testdata/object_reference_source_contract.json
var sourceReferenceProbe []byte

type referenceProfile struct {
	Kind         string `json:"kind"`
	UserSource   string `json:"userSource"`
	Observations []struct {
		Phase     string   `json:"phase"`
		SourceRaw string   `json:"sourceRaw"`
		CacheRaw  string   `json:"cacheRaw"`
		Keys      []string `json:"keys"`
		CacheKeys []string `json:"cacheKeys"`
	} `json:"observations"`
}

func referenceJSON(t *testing.T, object protocol.JSONObject) string {
	t.Helper()
	raw, err := object.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func referenceKeys(object protocol.JSONObject) []string {
	names := []string{}
	for _, p := range object.OwnProperties() {
		var name string
		_ = json.Unmarshal(p.NameJSON, &name)
		names = append(names, name)
	}
	return names
}
func TestJSONObjectReferenceSourceObservedRecordAliases(t *testing.T) {
	var document struct {
		Cases []referenceProfile `json:"cases"`
	}
	if err := json.Unmarshal(sourceReferenceProbe, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Cases) != 2 {
		t.Fatal("expected both actual Source record construction profiles")
	}
	for _, p := range document.Cases {
		if len(p.Observations) != 6 {
			t.Fatal("expected six actual Source observation points per profile")
		}
		t.Run(p.Kind, func(t *testing.T) {
			var user protocol.JSONObject
			if err := json.Unmarshal([]byte(p.UserSource), &user); err != nil {
				t.Fatal(err)
			}
			if p.Kind == "factory" {
				var api protocol.JSONObject
				if err := json.Unmarshal(user.Get("message"), &api); err != nil {
					t.Fatal(err)
				}
				factory, err := messages.NewFactory(func() string { t.Fatal("unexpected UUID allocation"); return "" }, func() time.Time { t.Fatal("unexpected time allocation"); return time.Time{} })
				if err != nil {
					t.Fatal(err)
				}
				user = messages.OrderedUser(factory.CreateUser(storage.Object{"content": api.Get("content"), "uuid": user.Get("uuid"), "timestamp": user.Get("timestamp")}))
			}
			// Source concat creates a new array retaining the original record reference.
			saved := []protocol.JSONObject{user}
			detached := user.Clone()
			initial := referenceJSON(t, detached)
			for _, o := range p.Observations {
				switch o.Phase {
				case "after-actual-stop-cache":
				case "assign-origin":
					user.Set("origin", json.RawMessage(`{"kind":"channel","server":"probe"}`))
				case "assign-isVirtual":
					user.Set("isVirtual", json.RawMessage(`true`))
				case "delete-isVirtual":
					user.Delete("isVirtual")
				case "delete-readd-origin":
					user.Delete("origin")
					user.Set("origin", json.RawMessage(`{"kind":"channel","server":"readded"}`))
				case "delete-readd-uuid-nonfinal-slot":
					uuid := user.Get("uuid")
					user.Delete("uuid")
					user.Set("uuid", uuid)
				default:
					t.Fatal("undeclared observed operation", o.Phase)
				}
				t.Run(o.Phase, func(t *testing.T) {
					if raw := referenceJSON(t, user); raw != o.SourceRaw {
						t.Errorf("original differs from actual Source: got %s want %s", raw, o.SourceRaw)
					}
					if raw := referenceJSON(t, saved[0]); raw != o.CacheRaw {
						t.Errorf("saved copied record lost Source reference: got %s want %s", raw, o.CacheRaw)
					}
					if keys := referenceKeys(user); !reflect.DeepEqual(keys, o.Keys) {
						t.Errorf("original keys %v want %v", keys, o.Keys)
					}
					if keys := referenceKeys(saved[0]); !reflect.DeepEqual(keys, o.CacheKeys) {
						t.Errorf("saved key order %v want %v", keys, o.CacheKeys)
					}
					if raw := referenceJSON(t, detached); raw != initial {
						t.Errorf("Clone retained original reference: %s", raw)
					}
				})
			}
		})
	}
}

func TestJSONObjectReferenceInitializedValueCopiesShareInsertDeleteAndAssign(t *testing.T) {
	original := protocol.NewJSONObject(protocol.JSONField{Name: "first", Value: json.RawMessage(`1`)}, protocol.JSONField{Name: "last", Value: json.RawMessage(`2`)})
	saved := original
	original.Set("future", json.RawMessage(`3`))
	if got := referenceJSON(t, saved); got != `{"first":1,"last":2,"future":3}` {
		t.Fatal(got)
	}
	saved.Delete("first")
	saved.Set("first", json.RawMessage(`4`))
	if got := referenceJSON(t, original); got != `{"last":2,"future":3,"first":4}` {
		t.Fatal(got)
	}
	extra := protocol.NewJSONObject(protocol.JSONField{Name: "undefined"}, protocol.JSONField{Name: "10", Value: json.RawMessage(`10`)}, protocol.JSONField{Name: "2", Value: json.RawMessage(`2`)})
	original.Assign(extra)
	if got := referenceJSON(t, saved); got != `{"2":2,"10":10,"last":2,"future":3,"first":4}` {
		t.Fatal(got)
	}
	if got := referenceKeys(saved); !reflect.DeepEqual(got, []string{"2", "10", "last", "future", "first", "undefined"}) {
		t.Fatal(got)
	}
	original.Assign(saved)
	original.Set("undefined", json.RawMessage(`null`))
	if got := referenceJSON(t, saved); got != `{"2":2,"10":10,"last":2,"future":3,"first":4,"undefined":null}` {
		t.Fatal(got)
	}
	if got := referenceJSON(t, extra); got != `{"2":2,"10":10}` {
		t.Fatal("Assign mutated source", got)
	}
}

func TestJSONObjectReferenceQuotedUTF16KeyIdentityAndEnumeration(t *testing.T) {
	original := protocol.NewJSONObject(protocol.JSONField{Name: "middle", Value: json.RawMessage(`true`)})
	saved := original
	for _, item := range []struct{ key, value string }{{`"\ud800"`, `1e400`}, {`"\ufffd"`, `null`}, {`"10"`, `10`}, {`"2"`, `2`}, {`"tail"`, `"\udfff"`}} {
		if err := original.SetPropertyJSON(json.RawMessage(item.key), json.RawMessage(item.value)); err != nil {
			t.Fatal(err)
		}
	}
	if got := referenceJSON(t, saved); got != `{"2":2,"10":10,"middle":true,"\ud800":1e400,"�":null,"tail":"\udfff"}` {
		t.Fatal(got)
	}
	if err := saved.DeletePropertyJSON(json.RawMessage(`"\ud800"`)); err != nil {
		t.Fatal(err)
	}
	if err := saved.SetPropertyJSON(json.RawMessage(`"\ud800"`), json.RawMessage(`false`)); err != nil {
		t.Fatal(err)
	}
	if err := original.SetPropertyJSON(json.RawMessage(`"t\u0061il"`), nil); err != nil {
		t.Fatal(err)
	}
	if got := referenceJSON(t, original); got != `{"2":2,"10":10,"middle":true,"�":null,"\ud800":false}` {
		t.Fatal(got)
	}
	props := saved.OwnProperties()
	if string(props[len(props)-1].NameJSON) != `"\ud800"` || string(props[len(props)-1].Value) != "false" {
		t.Fatal(props)
	}
	if string(props[len(props)-2].NameJSON) != `"tail"` || props[len(props)-2].Value != nil {
		t.Fatal(props)
	}
	before := referenceJSON(t, original)
	for _, key := range []string{`null`, `[]`, `"broken`} {
		if err := saved.SetPropertyJSON(json.RawMessage(key), json.RawMessage(`1`)); err == nil {
			t.Fatal("invalid key")
		}
		if err := saved.DeletePropertyJSON(json.RawMessage(key)); err == nil {
			t.Fatal("invalid key")
		}
	}
	if after := referenceJSON(t, original); after != before {
		t.Fatal(after)
	}
}

func TestJSONObjectReferenceCloneAndRawCopiesRetainIsolation(t *testing.T) {
	input := json.RawMessage(`{"lone":"\ud800","number":9007199254740993}`)
	original := protocol.NewJSONObject(protocol.JSONField{Name: "payload", Value: input})
	saved := original
	input[2] = 'X'
	got := saved.Get("payload")
	got[2] = 'Y'
	props := saved.OwnProperties()
	props[0].NameJSON[1] = 'Z'
	props[0].Value[2] = 'W'
	if got := referenceJSON(t, original); got != `{"payload":{"lone":"\ud800","number":9007199254740993}}` {
		t.Fatal(got)
	}
	detached := saved.Clone()
	detached.Set("payload", json.RawMessage(`null`))
	detached.Set("later", nil)
	detached.Delete("payload")
	detached.Set("payload", json.RawMessage(`false`))
	if got := referenceJSON(t, detached); got != `{"payload":false}` {
		t.Fatal(got)
	}
	if got := referenceJSON(t, original); got != `{"payload":{"lone":"\ud800","number":9007199254740993}}` {
		t.Fatal(got)
	}
	original.Set("new", json.RawMessage(`[]`))
	if got := referenceJSON(t, detached); got != `{"payload":false}` {
		t.Fatal(got)
	}
}

func TestJSONObjectReferenceZeroNullAndDecodedReplacementAreDistinct(t *testing.T) {
	var original protocol.JSONObject
	absentCopy := original
	original.Delete("missing")
	if err := original.DeletePropertyJSON(json.RawMessage(`"missing"`)); err != nil {
		t.Fatal(err)
	}
	if original.IsObject() || original.OwnProperties() == nil || len(original.OwnProperties()) != 0 || original.Clone().IsObject() {
		t.Fatal("zero became object")
	}
	original.Set("undefined", nil)
	if !original.IsObject() || referenceJSON(t, original) != "{}" || referenceJSON(t, absentCopy) != "null" {
		t.Fatal("zero value copies unexpectedly share future initialization")
	}
	saved := original
	if err := json.Unmarshal([]byte(`{"new":1,"\ud800":true}`), &original); err != nil {
		t.Fatal(err)
	}
	if referenceJSON(t, saved) != "{}" || referenceJSON(t, original) != `{"new":1,"\ud800":true}` {
		t.Fatal("decode mutated prior object")
	}
	before := original
	if err := json.Unmarshal([]byte(`{"bad":`), &original); err == nil {
		t.Fatal("bad JSON")
	}
	if referenceJSON(t, original) != referenceJSON(t, before) {
		t.Fatal("failed decode altered shared state")
	}
	if err := json.Unmarshal([]byte(`null`), &original); err != nil {
		t.Fatal(err)
	}
	if original.IsObject() || before.Get("new") == nil {
		t.Fatal("null decode destroyed older shared object")
	}
}

func TestJSONObjectReferenceOwnSnapshotsPermitIndependentConcurrentReads(t *testing.T) {
	original := protocol.NewJSONObject(protocol.JSONField{Name: "data", Value: json.RawMessage(`"value"`)})
	saved := original
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			props := saved.OwnProperties()
			props[0].NameJSON[1] = 'X'
			props[0].Value[1] = 'X'
			copy := saved.Clone()
			copy.Set("new", json.RawMessage(`true`))
		}()
	}
	workers.Wait()
	if referenceJSON(t, original) != `{"data":"value"}` {
		t.Fatal("read snapshot shared mutable buffers")
	}
	// This does not claim concurrent mutation safety; no reference lock is added.
}
