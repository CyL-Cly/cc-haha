package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceUUIDLoadCacheAndCompleteTranscript(t *testing.T) {
	sourceBytes, e := os.ReadFile("testdata/uuid_source_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var contract struct {
		Source string
		Cases  []struct {
			Name     string
			Original struct {
				Profile struct {
					Operation, Fault string
					Disabled         bool
				}
				Content, SID string
				Probes       []struct {
					Missing bool
					Value   json.RawMessage
				}
				RewriteBytes *string
			}
			Expected struct {
				Held    struct{ FirstSettled, FlushSettled bool }
				Results []struct {
					Status string
					Value  bool
				}
				Hot, AfterClear []bool
				Keys, Maps      json.RawMessage
			}
		}
	}
	if e = json.Unmarshal(sourceBytes, &contract); e != nil {
		t.Fatal(e)
	}
	if contract.Source != "Bun 1.3.14 actual original UUID load/cache Source self2" || len(contract.Cases) != 12 {
		t.Fatal("actual Source capture required")
	}
	for _, sample := range contract.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			original := sample.Original
			io := &uuidIO{kind: original.Profile.Operation, fault: original.Profile.Fault, entered: make(chan struct{}), released: make(chan struct{})}
			owner, writer, pending, loader := uuidHarness(t, original.Content, io, original.Profile.Disabled, original.SID)
			future := uuidBegin(t, owner, pending, loader, original.SID)
			awaitSignal(t, io.entered, "original Source operation")
			if other := uuidBegin(t, owner, pending, loader, original.SID); other != future {
				t.Fatal("coalesced promise identity lost")
			}
			flush := beginPending(t, owner, pending)
			if e := flush.Wait(context.Background()); e != nil {
				t.Fatal(e)
			}
			settled := false
			select {
			case <-future.Done():
				settled = true
			default:
			}
			if settled != sample.Expected.Held.FirstSettled || !sample.Expected.Held.FlushSettled {
				t.Fatal("actual held/flush receipt changed")
			}
			close(io.released)
			set, e := future.Wait(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			probe := func(index int) json.RawMessage {
				if original.Probes[index].Missing {
					return nil
				}
				return original.Probes[index].Value
			}
			for index, want := range sample.Expected.Results {
				if want.Status != "fulfilled" || set.Has(probe(index)) != want.Value {
					t.Fatal("original cold lookup", index, want)
				}
			}
			if original.RewriteBytes != nil {
				if e := os.WriteFile(writer.target(), []byte(*original.RewriteBytes), 0600); e != nil {
					t.Fatal(e)
				}
			}
			hot := uuidBegin(t, owner, pending, loader, original.SID)
			if hot != future {
				t.Fatal("hot memoized promise changed")
			}
			for index, want := range sample.Expected.Hot {
				if set.Has(probe(index)) != want {
					t.Fatal("original hot lookup", index)
				}
			}
			pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { return pending.ClearSessionUUIDCache(s) })
			after := uuidBegin(t, owner, pending, loader, original.SID)
			set, e = after.Wait(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			for index, want := range sample.Expected.AfterClear {
				if set.Has(probe(index)) != want {
					t.Fatal("original cleared lookup", index)
				}
			}
			// This synchronous replay exercises the mechanically shared scanner and
			// exact reducer; it is not an async worker or a second production loader.
			value, e := loader.LoadTranscriptFile(writer.target(), false)
			if e != nil {
				t.Fatal(e)
			}
			actual, e := value.MarshalJSON()
			if e != nil {
				t.Fatal(e)
			}
			actual, e = protocol.ParseAndStringifyJavaScriptJSON(actual)
			if e != nil {
				t.Fatal(e)
			}
			var expected bytes.Buffer
			if e = json.Compact(&expected, sample.Expected.Maps); e != nil {
				t.Fatal(e)
			}
			if string(actual) != expected.String() {
				t.Fatalf("full original transcript differs\nactual %.2000s\nsource %.2000s", actual, sample.Expected.Maps)
			}
			keys := []protocol.JSONObject{}
			for _, k := range set.Keys() {
				raw := k.Raw()
				v := protocol.NewJSONObject(protocol.JSONField{Name: "missing", Value: rawBoolean(raw == nil)})
				if raw != nil {
					serial, e := protocol.ParseAndStringifyJavaScriptJSON(raw)
					if e != nil {
						t.Fatal(e)
					}
					v.Set("raw", quote(string(serial)))
				}
				keys = append(keys, v)
			}
			encoded, e := json.Marshal(keys)
			if e != nil {
				t.Fatal(e)
			}
			expected.Reset()
			if e = json.Compact(&expected, sample.Expected.Keys); e != nil {
				t.Fatal(e)
			}
			if string(encoded) != expected.String() {
				t.Fatalf("actual Map keys differ: %s / %s", encoded, sample.Expected.Keys)
			}
		})
	}
}
func rawBoolean(value bool) json.RawMessage {
	if value {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}
