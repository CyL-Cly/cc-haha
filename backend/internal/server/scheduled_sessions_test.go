package server

import (
	"context"
	"os"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestScheduledManualSessionUsesActualPlaceholderAndCanonicalMetadata(t *testing.T) {
	s, _, _, _ := consumerFixture(t, "off")
	ctx := context.Background()
	id, e := s.CreateScheduledSession(ctx, s.options.WorkDir, "bypassPermissions")
	if e != nil {
		t.Fatal(e)
	}
	info, e := s.findSession(ctx, id)
	if e != nil || info.PermissionMode != "bypassPermissions" {
		t.Fatalf("actual launch%+v %v", info, e)
	}
	if e = s.DeleteScheduledPlaceholder(ctx, id); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(info.Path); !os.IsNotExist(e) {
		t.Fatal("placeholder not physically removed", e)
	}
	// Simulate the ordinary writer's actual conversation at the same SID/path.
	data := []byte(`{"type":"user","uuid":"scheduled-first-user","sessionId":"` + id + `","cwd":"` + s.options.WorkDir + `","timestamp":"2026-10-07T00:00:00.000Z","message":{"role":"user","content":"actual scheduled prompt"}}` + "\n")
	if e = os.WriteFile(info.Path, data, 0600); e != nil {
		t.Fatal(e)
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "workDir", Value: []byte(strconvQuote(s.options.WorkDir))}, protocol.JSONField{Name: "permissionMode", Value: []byte(`"bypassPermissions"`)})
	if e = s.AppendScheduledSessionMetadata(ctx, id, metadata); e != nil {
		t.Fatal(e)
	}
	actual, e := s.findSession(ctx, id)
	if e != nil || actual.PermissionMode != "bypassPermissions" || actual.WorkDir != s.options.WorkDir {
		t.Fatalf("canonical metadata%+v %v", actual, e)
	}
	first, e := os.ReadFile(info.Path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AppendScheduledSessionMetadata(ctx, id, metadata); e != nil {
		t.Fatal(e)
	}
	second, e := os.ReadFile(info.Path)
	if e != nil || string(first) != string(second) {
		t.Fatal("unchanged launch metadata appended duplicate record", e)
	}
	if e = s.DeleteScheduledPlaceholder(ctx, "00000000-0000-4000-8000-000000000000"); e != nil {
		t.Fatal("absent placeholder should be no-op", e)
	}
}

func TestScheduledRetentionZeroKeepsOnlyOriginalMemoryLaunch(t *testing.T) {
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	o.PersistSession = func(context.Context) (bool, error) { return false, nil }
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	id, e := s.CreateScheduledSession(context.Background(), o.WorkDir, "bypassPermissions")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteScheduledPlaceholder(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "workDir", Value: []byte(strconvQuote(o.WorkDir))}, protocol.JSONField{Name: "permissionMode", Value: []byte(`"bypassPermissions"`)})
	if e = s.AppendScheduledSessionMetadata(context.Background(), id, metadata); e != nil {
		t.Fatal(e)
	}
	info, e := s.findSession(context.Background(), id)
	if e != nil || !info.memory || info.PermissionMode != "bypassPermissions" {
		t.Fatalf("memory launch%+v %v", info, e)
	}
	if _, e = os.Stat(info.Path); !os.IsNotExist(e) {
		t.Fatal("retention-zero schedule wrote transcript", e)
	}
}
