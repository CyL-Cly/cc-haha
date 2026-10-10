package workflowcommands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/javascriptparse"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceRenameInvalidOriginalNames(t *testing.T) {
	raw, e := os.ReadFile("testdata/acorn_metadata_rename_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Input    struct{ Script, RenameName json.RawMessage }
		Expected struct{ Rename json.RawMessage }
	}
	if e = json.Unmarshal(raw, &rows); e != nil || len(rows) != 10 {
		t.Fatal(e, len(rows))
	}
	withMetadata(t, func(s *cooperative.Scope, m *Metadata) {
		for _, row := range rows {
			script, e := protocol.DecodeJavaScriptJSONString(row.Input.Script)
			if e != nil {
				t.Fatal(e)
			}
			name, e := protocol.DecodeJavaScriptJSONString(row.Input.RenameName)
			if e != nil {
				t.Fatal(e)
			}
			renamed, result, e := m.Rename(s, script, name)
			if e != nil {
				t.Fatal(e)
			}
			actual := result.ResultJSON()
			if result.ErrorUnits == nil {
				obj := protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: protocol.QuoteJavaScriptUTF16(renamed)})
				actual, _ = obj.MarshalJSON()
			}
			if !bytes.Equal(actual, protocol.StringifyValidatedJavaScriptJSON(row.Expected.Rename)) {
				t.Fatalf("rename original name bytes differ: got %s want %s", actual, row.Expected.Rename)
			}
		}
	})
}

func TestMetadataCancelledScopeDoesNotPoisonParser(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller cancelled before language operation")
	var m *Metadata
	err := owner.Run(ctx, func(s *cooperative.Scope) error {
		var e error
		m, e = NewMetadata(s, owner)
		if e != nil {
			return e
		}
		cancel(cause)
		if _, e = m.ParseScript(s, "export const meta={name:'a',description:'d'};"); !errors.Is(e, cause) {
			t.Error(e)
		}
		if _, _, e = m.Rename(s, units("export const meta={name:'a',description:'d'};"), units("new")); !errors.Is(e, cause) {
			t.Error(e)
		}
		if _, e = m.UsesBanned(s, units("Date.now()")); !errors.Is(e, cause) {
			t.Error(e)
		}
		if _, e = NewMetadata(s, owner); !errors.Is(e, cause) {
			t.Error(e)
		}
		return nil
	})
	if err != nil && !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		r, e := m.ParseScript(s, "export const meta={name:'a',description:'d'};")
		if e != nil || r.ErrorUnits != nil {
			t.Error(e, r.Error)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}

func TestCancelledConstructionAndOriginalPluginRefusal(t *testing.T) {
	h := newHost(t)
	h.run(t, func(s *cooperative.Scope) error {
		options := h.service.options
		options.OriginalInlinePlugins = []string{"private-plugin"}
		_, e := New(s, options)
		var cap *CapabilityError
		if !errors.As(e, &cap) {
			t.Error(e)
		}
		return nil
	})
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller cancellation before service initialization")
	err := h.owner.Run(ctx, func(s *cooperative.Scope) error {
		cancel(cause)
		if _, e := New(s, h.service.options); !errors.Is(e, cause) {
			t.Error(e)
		}
		h.service.bundled = nil
		if _, e := h.service.bundledDefinitions(s); !errors.Is(e, cause) {
			t.Error(e)
		}
		return nil
	})
	if err != nil && !errors.Is(err, cause) {
		t.Fatal(err)
	}
}

func TestCapabilityCachedUntilActualInvalidationThenRealRecovery(t *testing.T) {
	h := newHost(t)
	path := filepath.Join(h.config, "workflows", "original.js")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("export const meta={name:'reg',description:'d',unknown:/[\\p{ASCII}&&\\p{Letter}]/v};"), 0600); e != nil {
		t.Fatal(e)
	}
	h.run(t, func(s *cooperative.Scope) error {
		first, e := h.service.BeginCommands(s, h.cwd)
		if e != nil {
			return e
		}
		_, e = first.Await(s)
		var cap *javascriptparse.CapabilityError
		if !errors.As(e, &cap) {
			t.Error("actual constructor gap must reject", e)
		}
		writeWorkflow(t, path, "recovered", "actual physical edit")
		again, e := h.service.BeginCommands(s, h.cwd)
		if e != nil {
			return e
		}
		if first != again {
			t.Error("rejected original promise replaced")
		}
		_, e = again.Await(s)
		if !errors.As(e, &cap) {
			t.Error(e)
		}
		if e = h.service.ClearCommandCache(s); e != nil {
			return e
		}
		next, e := h.service.BeginCommands(s, h.cwd)
		if e != nil {
			return e
		}
		values, e := next.Await(s)
		if e != nil {
			return e
		}
		if len(values) != 2 || values[1].Name() != "recovered" {
			t.Error(names(values))
		}
		return nil
	})
}

func TestIndependentPhysicalCompletionAfterOwnerClose(t *testing.T) {
	owner := cooperative.New()
	path := filepath.Join(t.TempDir(), "private-bytes")
	if e := os.WriteFile(path, []byte("private immutable IO"), 0600); e != nil {
		t.Fatal(e)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var future *ownerasync.OwnerFuture[[]byte]
	completed := make(chan error, 1)
	go func() {
		completed <- owner.Run(context.Background(), func(s *cooperative.Scope) error {
			future = ioFuture(&Service{options: Options{Owner: owner}}, s, func() ([]byte, error) { close(started); <-release; return os.ReadFile(path) })
			_, e := future.Await(s)
			return e
		})
	}()
	<-started
	owner.Close()
	if e := <-completed; !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal(e)
	}
	close(release)
	if value, e := future.Wait(context.Background()); !errors.Is(e, cooperative.ErrClosed) || value != nil {
		t.Fatal(string(value), e)
	}
	if raw, e := os.ReadFile(path); e != nil || string(raw) != "private immutable IO" {
		t.Fatal(e, string(raw))
	}
}

func TestNativeEmptyDirectoryAndNestedRepositoryBoundary(t *testing.T) {
	h := newHost(t)
	if e := os.MkdirAll(filepath.Join(h.config, "workflows"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(h.cwd, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	writeWorkflow(t, filepath.Join(h.home, "repo", ".claude", "workflows", "p.js"), "outer", "outer boundary")
	h.run(t, func(s *cooperative.Scope) error {
		f, e := h.service.BeginDefinitions(s, nil)
		if e != nil {
			return e
		}
		values, e := f.Await(s)
		if e != nil {
			return e
		}
		if len(values) != 2 {
			t.Error(values)
		}
		return nil
	})
}
