package signalerror_test

import (
	"errors"
	"testing"

	"go.mau.fi/libsignal/groups/state/record"
	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/signalerror"
)

func TestInitializedSentinelsRemainStableInSignalConsumers(t *testing.T) {
	sender := record.NewSenderKey(nil, nil)
	for attempt := 0; attempt < 2; attempt++ {
		_, err := sender.SenderKeyState()
		if err != signalerror.ErrNoSenderKeyStatesInRecord || err.Error() != "no sender key states in record" {
			t.Fatal("empty sender record lost its stable error", err)
		}
		_, err = sender.GetSenderKeyStateByID(42)
		if !errors.Is(err, signalerror.ErrNoSenderKeyStateForID) || err.Error() != "no sender key state for key ID 42" {
			t.Fatal("missing sender state lost its wrapped sentinel", err)
		}
	}
	for _, tc := range []struct {
		version  int
		sentinel error
		message  string
	}{
		{protocol.UnsupportedVersion, signalerror.ErrOldMessageVersion, "too old message version 1 (normal message)"},
		{protocol.CurrentVersion + 1, signalerror.ErrUnknownMessageVersion, "unknown message version 4 (normal message)"},
		{protocol.CurrentVersion, signalerror.ErrIncompleteMessage, "incomplete message (normal message)"},
	} {
		for attempt := 0; attempt < 2; attempt++ {
			_, err := protocol.NewSignalMessageFromStruct(&protocol.SignalMessageStructure{Version: tc.version}, nil)
			if !errors.Is(err, tc.sentinel) || err.Error() != tc.message {
				t.Fatal("signal validation lost its wrapped error", tc.version, err)
			}
		}
	}
	if signalerror.ErrNoSenderKeyStatesInRecord == signalerror.ErrNoSenderKeyStateForID || signalerror.ErrOldMessageVersion == signalerror.ErrUnknownMessageVersion || signalerror.ErrIncompleteMessage == signalerror.ErrBadMAC {
		t.Fatal("distinct signal sentinels became aliases")
	}
}
