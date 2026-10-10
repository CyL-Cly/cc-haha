package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestMaxTurnsInvalidAttachmentCannotReadOrChargeLedger(t *testing.T) {
	// Invalid internal envelopes must fail before accounting or UUID/clock reads.
	h := Host{options: Options{UUID: func() string { t.Error("invalid attachment allocated terminal identity"); return "" }, NowMillis: func() float64 { t.Error("invalid attachment sampled clock"); return 0 }}}
	for _, value := range []string{
		`{"type":"assistant","attachment":{"type":"max_turns_reached","maxTurns":1,"turnCount":2}}`,
		`{"type":"attachment","attachment":{"type":"other","maxTurns":1,"turnCount":2}}`,
		`{"type":"attachment","attachment":{"type":"max_turns_reached","maxTurns":null,"turnCount":2}}`,
		`{"type":"attachment","attachment":{"type":"max_turns_reached","maxTurns":0,"turnCount":1}}`,
		`{"type":"attachment","attachment":{"type":"max_turns_reached","maxTurns":1,"turnCount":1}}`,
	} {
		var record protocol.JSONObject
		if err := json.Unmarshal([]byte(value), &record); err != nil {
			t.Fatal(err)
		}
		_, err := h.maxTurnsResultOnOwner(nil, queryloop.Message{Record: record}, zeroUsage(), json.RawMessage("null"), 0)
		if err == nil || err.Error() != "invalid original max-turn attachment" {
			t.Fatal("invalid producer envelope projected", value, err)
		}
	}
}
func TestMaxTurnsResultRequiresOriginalAccountingOwner(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	ledger, err := modelaccounting.NewOwnedSession(owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Host{sessionAccounting: ledger}
	record := object([]byte(`{"type":"attachment","attachment":{"type":"max_turns_reached","maxTurns":1,"turnCount":2}}`))
	err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := h.maxTurnsResultOnOwner(scope, queryloop.Message{Record: record}, zeroUsage(), json.RawMessage("null"), 0)
		if !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("max-turn result borrowed another Owner accounting")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
