package resumestate

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicOwnedSIDLoadsActualNativeTranscript(t *testing.T) {
	s, owner, _ := publicService(t, OSFiles{})
	s.options.PublicSID = true
	path := filepath.Join(s.projectDir(), oldSID+".jsonl")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	content := `{"parentUuid":null,"isSidechain":false,"type":"user","uuid":"u-own","timestamp":"2023-11-14T22:13:20.000Z","sessionId":"` + oldSID + `","message":{"role":"user","content":"own native prompt"}}` + "\n"
	if e := os.WriteFile(path, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	var got *Result
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, e := s.BeginLoad(scope, Input{Source: quote(oldSID)})
		if e != nil {
			return e
		}
		got, e = f.Await(scope)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if got == nil || len(got.Messages) < 1 || text(got.Metadata.Get("sessionId")) != oldSID {
		t.Fatalf("actual disk selection not restored: %+v", got)
	}
	if text(got.Messages[0].Field("type")) != "user" || text(object(got.Messages[0].Field("message")).Get("content")) != "own native prompt" {
		t.Fatal("native history content lost", got.Messages)
	}
	if len(got.Messages) != 2 || text(got.Messages[1].Field("type")) != "assistant" {
		t.Fatal("original interrupted-prompt reducer append changed", got.Messages)
	}
	if s.UUIDTranscript(oldSID) == nil {
		t.Fatal("original native loader cache not primed")
	}
}
