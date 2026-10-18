package mcpconfig

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDiagnosticsRetainsReadFailureAndCancellation(t *testing.T) {
	s, home, _, _, _, _, _ := fixture(t)
	identity := errors.New("actual scoped config read failed")
	s.options.Global = func(context.Context) (protocol.JSONObject, error) { return protocol.JSONObject{}, identity }
	if _, err := s.Diagnostics(context.Background(), home+"/global.json"); err != identity {
		t.Fatal("diagnostic read failure identity changed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Diagnostics(ctx, home+"/global.json"); err != context.Canceled {
		t.Fatal("canceled diagnostic performed scoped reads", err)
	}
}
