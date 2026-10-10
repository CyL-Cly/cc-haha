package workflowcommands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/javascriptparse"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"reflect"
	"testing"
)

func withMetadata(t *testing.T, fn func(*cooperative.Scope, *Metadata)) {
	t.Helper()
	owner := cooperative.New()
	defer owner.Close()
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		m, e := NewMetadata(scope, owner)
		if e != nil {
			return e
		}
		fn(scope, m)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestActualSourceMetadataAndSpan(t *testing.T) {
	raw, e := os.ReadFile("testdata/metadata_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Input struct {
			ID     string
			Script json.RawMessage
		}
		Expected   json.RawMessage
		SourceSpan *struct{ Start, End int }
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		t.Fatal(e)
	}
	if len(rows) != 134 {
		t.Fatal(len(rows))
	}
	withMetadata(t, func(scope *cooperative.Scope, m *Metadata) {
		for _, row := range rows {
			t.Run(row.Input.ID, func(t *testing.T) {
				u, e := protocol.DecodeJavaScriptJSONString(row.Input.Script)
				if e != nil {
					t.Fatal(e)
				}
				got, e := m.Parse(scope, u)
				var cap *javascriptparse.CapabilityError
				if errors.As(e, &cap) {
					if row.Input.ID != "unicode-regexp" {
						t.Fatalf("unexpected constructor gap %s %v", row.Input.ID, e)
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				var actual, expected any
				actualRaw := got.ResultJSON()
				_ = json.Unmarshal(actualRaw, &actual)
				_ = json.Unmarshal(row.Expected, &expected)
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("Source mismatch got %s want %s", actualRaw, row.Expected)
				}
				if got.ErrorUnits == nil {
					if row.SourceSpan == nil || got.EndUTF16 != row.SourceSpan.End {
						t.Fatalf("span %+v %+v", got, row.SourceSpan)
					}
					var expectedFields protocol.JSONObject
					_ = expectedFields.UnmarshalJSON(row.Expected)
					actualMeta, _ := got.Meta.MarshalJSON()
					if !bytes.Equal(actualMeta, protocol.StringifyValidatedJavaScriptJSON(expectedFields.Get("meta"))) {
						t.Fatalf("meta order got %s want %s", actualMeta, expectedFields.Get("meta"))
					}
				}
			})
		}
	})
}
func TestWholeModuleSyntaxIsValidatedBeforeMetadata(t *testing.T) {
	withMetadata(t, func(scope *cooperative.Scope, m *Metadata) {
		for _, script := range []string{`export const meta={name:'a',description:'d'}; let x='\xG0'`, `export const meta={name:'a',description:'d'}; let x='\uGGGG'`, `export const meta={name:'a',description:'d'}; /[/;`} {
			r, e := m.ParseScript(scope, script)
			if e != nil || r.ErrorUnits == nil || !bytes.HasPrefix(r.ResultJSON(), []byte(`{"error":"Script parse error:`)) {
				t.Fatalf("whole grammar accepted %q %+v %v", script, r, e)
			}
		}
	})
}
func TestMetadataScopeAndNativeDecoder(t *testing.T) {
	other := cooperative.New()
	defer other.Close()
	withMetadata(t, func(scope *cooperative.Scope, m *Metadata) {
		if _, e := NewMetadata(nil, m.owner); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal(e)
		}
		if _, e := m.Parse(nil, units("")); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal(e)
		}
		if e := other.Run(context.Background(), func(foreign *cooperative.Scope) error {
			_, e := m.Parse(foreign, nil)
			if !errors.Is(e, cooperative.ErrRestricted) {
				t.Fatal(e)
			}
			_, _, e = m.Rename(foreign, nil, nil)
			if !errors.Is(e, cooperative.ErrRestricted) {
				t.Fatal(e)
			}
			_, e = m.UsesBanned(foreign, nil)
			if !errors.Is(e, cooperative.ErrRestricted) {
				t.Fatal(e)
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if _, e := m.ParseScript(scope, string([]byte{0xff})); e == nil {
			t.Fatal("invalid native decoder admitted")
		}
	})
}

func TestOriginalSourceAcornConsumerRegressions(t *testing.T) {
	raw, e := os.ReadFile("testdata/acorn_metadata_regression_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Input struct {
			ID     string
			Script json.RawMessage
		}
		Expected struct {
			Result json.RawMessage
			Rename json.RawMessage
			Banned bool
			Bytes  int
		}
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		t.Fatal(e)
	}
	if len(rows) != 55 {
		t.Fatal(len(rows))
	}
	withMetadata(t, func(scope *cooperative.Scope, m *Metadata) {
		for _, row := range rows {
			t.Run(row.Input.ID, func(t *testing.T) {
				u, e := protocol.DecodeJavaScriptJSONString(row.Input.Script)
				if e != nil {
					t.Fatal(e)
				}
				r, e := m.Parse(scope, u)
				if e != nil {
					t.Fatal(e)
				}
				var got, want any
				_ = json.Unmarshal(r.ResultJSON(), &got)
				_ = json.Unmarshal(row.Expected.Result, &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("original Source parse result mismatch id=%s", row.Input.ID)
				}
				if ScriptByteLength(u) != row.Expected.Bytes {
					t.Fatalf("actual Bun byteLength %d want %d", ScriptByteLength(u), row.Expected.Bytes)
				}
				renamed, renameResult, e := m.Rename(scope, u, units("renamed-valid"))
				if e != nil {
					t.Fatal(e)
				}
				var renameRaw json.RawMessage
				if renameResult.ErrorUnits != nil {
					renameRaw = renameResult.ResultJSON()
				} else {
					obj := protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: protocol.QuoteJavaScriptUTF16(renamed)})
					renameRaw, _ = obj.MarshalJSON()
				}
				_ = json.Unmarshal(renameRaw, &got)
				_ = json.Unmarshal(row.Expected.Rename, &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("Source rename mismatch id=%s", row.Input.ID)
				}
				banned, e := m.UsesBanned(scope, u)
				if e != nil || banned != row.Expected.Banned {
					t.Fatal(banned, row.Expected.Banned, e)
				}
			})
		}
	})
}

func TestOriginalSourceLiteralPurityRegressions(t *testing.T) {
	raw, e := os.ReadFile("testdata/acorn_metadata_purity_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Input struct {
			ID     string
			Script json.RawMessage
		}
		Expected struct {
			Result json.RawMessage
			Rename json.RawMessage
			Banned bool
			Bytes  int
		}
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		t.Fatal(e)
	}
	if len(rows) != 47 {
		t.Fatal(len(rows))
	}
	withMetadata(t, func(scope *cooperative.Scope, m *Metadata) {
		for _, row := range rows {
			t.Run(row.Input.ID, func(t *testing.T) {
				u, e := protocol.DecodeJavaScriptJSONString(row.Input.Script)
				if e != nil {
					t.Fatal(e)
				}
				r, e := m.Parse(scope, u)
				if e != nil {
					t.Fatal(e)
				}
				var got, want any
				_ = json.Unmarshal(r.ResultJSON(), &got)
				_ = json.Unmarshal(row.Expected.Result, &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("original Source parse result mismatch id=%s", row.Input.ID)
				}
				if ScriptByteLength(u) != row.Expected.Bytes {
					t.Fatalf("actual Bun byteLength %d want %d", ScriptByteLength(u), row.Expected.Bytes)
				}
				renamed, renameResult, e := m.Rename(scope, u, units("renamed-valid"))
				if e != nil {
					t.Fatal(e)
				}
				var renameRaw json.RawMessage
				if renameResult.ErrorUnits != nil {
					renameRaw = renameResult.ResultJSON()
				} else {
					obj := protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: protocol.QuoteJavaScriptUTF16(renamed)})
					renameRaw, _ = obj.MarshalJSON()
				}
				_ = json.Unmarshal(renameRaw, &got)
				_ = json.Unmarshal(row.Expected.Rename, &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("Source rename mismatch id=%s", row.Input.ID)
				}
				banned, e := m.UsesBanned(scope, u)
				if e != nil || banned != row.Expected.Banned {
					t.Fatal(banned, row.Expected.Banned, e)
				}
			})
		}
	})
}
