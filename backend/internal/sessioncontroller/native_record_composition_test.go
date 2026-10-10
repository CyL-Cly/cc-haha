package sessioncontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Native component composition, not a full SDK or Source event-loop oracle.
// A real independently admitted Record settles during the same Owner's native
// fetch await, before the fixture permits any response header bytes.
func TestCompositionRecordFinallyProgressesDuringActualNativeFetch(t *testing.T) {
	files := newPreparationFiles("stat", "")
	owner, writer, pending, loader := recordHost(t, files, nil)
	t.Cleanup(files.unblock)
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("record-during-fetch", "original private message")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
	awaitSignal(t, files.held, "real Record fstat")
	received, release := make(chan struct{}), make(chan struct{})
	released := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/messages" || string(body) != `{"original":"request"}` {
			t.Error("physical input changed", r.URL.Path, string(body))
		}
		close(received)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"original":"response"}`)
	}))
	defer server.Close()
	defer func() {
		if !released {
			close(release)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		returned <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			driver, err := providernative.New(owner, scope, nil)
			if err != nil {
				return err
			}
			client := provider.NewAnthropic(provider.AnthropicConfig{APIKey: "offline-key", BaseURL: server.URL, Timeout: 4 * time.Second, HTTPExchange: &provider.RawHTTP1Exchange{}, OwnerNativeIO: driver})
			response, done, err := client.OpenPreparedResponse(ctx, &provider.MessagesRequest{Body: []byte(`{"original":"request"}`), Path: "/v1/messages", Headers: http.Header{}})
			if err != nil {
				return err
			}
			defer done()
			defer response.Response.Body.Close()
			bytes, err := scope.AwaitBytes(cooperative.StreamNext, func(context.Context) ([]byte, error) { return io.ReadAll(response.Response.Body) })
			if err != nil {
				return err
			}
			if string(bytes) != `{"original":"response"}` {
				return fmt.Errorf("response changed: %s", bytes)
			}
			return nil
		})
	}()
	awaitSignal(t, received, "actual native physical request")
	receipt, err := owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error { files.hold = ""; files.unblock(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err = future.Wait(ctx); err != nil {
		t.Fatal("native fetch blocked independent Record finally", err)
	}
	select {
	case err := <-returned:
		t.Fatal("fetch returned before original header release", err)
	default:
	}
	close(release)
	released = true
	if err = <-returned; err != nil {
		t.Fatal(err)
	}
	if err = flushRecord(t, owner, pending).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if pending.tracked != 0 {
			return fmt.Errorf("finally count=%d", pending.tracked)
		}
		return nil
	})
	bytes := recordBytes(t, writer)
	if strings.Count(bytes, `"uuid":"record-during-fetch"`) != 1 || !strings.Contains(bytes, "original private message") {
		t.Fatal("actual Record dropped/duplicated", bytes)
	}
}
