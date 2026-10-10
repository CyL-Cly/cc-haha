package imruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTelegramOriginalGrammyPollingSetupRetryMenuPacketsAndPhysicalClose(t *testing.T) {
	want := runIMSource(t, "native-telegram-api-poll.fixture.ts", "CC_HAHA_TELEGRAM_API_POLL_FIXTURE", map[string]any{}, "")
	var mu sync.Mutex
	calls := []protocol.JSONObject{}
	me, webhook, polls := 0, 0, 0
	blocked := make(chan struct{})
	aborted := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload protocol.JSONObject
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		calls = append(calls, protocol.NewJSONObject(protocol.JSONField{Name: "method", Value: raw(method)}, protocol.JSONField{Name: "payload", Value: raw(payload)}))
		reply := any(true)
		failure := any(nil)
		wait := false
		switch method {
		case "getMe":
			me++
			if me == 1 {
				failure = map[string]any{"ok": false, "error_code": 500, "description": "private setup retry"}
			} else {
				reply = map[string]any{"id": 999, "is_bot": true, "username": "private_test_bot", "first_name": "Private"}
			}
		case "deleteWebhook":
			webhook++
			if webhook == 1 {
				failure = map[string]any{"ok": false, "error_code": 429, "description": "private setup throttle", "parameters": map[string]any{"retry_after": 0}}
			}
		case "getUpdates":
			if string(payload.Get("limit")) == "1" {
				reply = []any{}
			} else {
				polls++
				switch polls {
				case 1:
					failure = map[string]any{"ok": false, "error_code": 429, "description": "private poll throttle", "parameters": map[string]any{"retry_after": 0}}
				case 2:
					reply = []any{map[string]any{"update_id": 7, "message": map[string]any{"message_id": 10, "date": 1, "from": map[string]any{"id": 42, "is_bot": false, "first_name": "Fixture"}, "chat": map[string]any{"id": 42, "type": "private"}, "text": "first"}}, map[string]any{"update_id": 9, "message": map[string]any{"message_id": 11, "date": 1, "from": map[string]any{"id": 42, "is_bot": false, "first_name": "Fixture"}, "chat": map[string]any{"id": 42, "type": "private"}, "text": "second"}}}
				default:
					wait = true
					close(blocked)
				}
			}
		case "sendMessage":
			reply = map[string]int{"message_id": 100}
		}
		mu.Unlock()
		if wait {
			<-r.Context().Done()
			close(aborted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if failure != nil {
			_, _ = w.Write(raw(failure))
		} else {
			_, _ = w.Write(raw(map[string]any{"ok": true, "result": reply}))
		}
	}))
	defer peer.Close()
	api, err := NewTelegramAPI(TelegramAPIOptions{Token: "private-token-never-sent", APIRoot: peer.URL, HTTP: peer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err = SyncTelegramCommands(context.Background(), api); err != nil {
		t.Fatal(err)
	}
	poller := &TelegramPoller{API: api, Handler: func(ctx context.Context, update protocol.JSONObject) error {
		message := object(update.Get("message"))
		_, err := api.SendMessage(ctx, "42", NewText("received "+message.String("text")))
		return err
	}}
	poller.Start(context.Background())
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("actual long poll did not arrive")
	}
	poller.Close()
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the actual HTTP request")
	}
	if err = poller.Wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	initial, rest := []protocol.JSONObject{}, []protocol.JSONObject{}
	for _, call := range calls {
		if method := call.String("method"); method == "getMe" || method == "deleteWebhook" {
			initial = append(initial, call)
		} else {
			rest = append(rest, call)
		}
	}
	mu.Unlock()
	sort.SliceStable(initial, func(i, j int) bool { return initial[i].String("method") < initial[j].String("method") })
	exactJSON(t, raw(map[string]any{"initial": initial, "rest": rest, "blockedAborted": true}), want, "actual polling")
}
