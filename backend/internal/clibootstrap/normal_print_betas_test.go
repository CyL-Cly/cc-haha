package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Exercise the normal caller's real parser, Owner, Controller and Writer with
// a shipped local command. No Source captures or provider response supplies init.
func TestNormalPrintCallerDefaultSDKBetaIsOmitted(t *testing.T) {
	n := nativeForTest(t)
	args, err := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	p, err := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"/cost\"}}\n"), Output: &out, Error: &errOut})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	code, err := p.Run(context.Background())
	if err != nil || code != 0 || p.Host == nil || p.Controller == nil || p.Owner == nil {
		t.Fatal("normal shared caller failed", code, err, errOut.String(), out.String())
	}
	inits := 0
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatal("invalid actual stdout", err, string(line))
		}
		if string(message["type"]) == `"stream_event"` {
			t.Fatal("local command unexpectedly invoked model producer", string(line))
		}
		if string(message["type"]) != `"system"` || string(message["subtype"]) != `"init"` {
			continue
		}
		inits++
		t.Logf("actual normal caller init: %s", line)
		if _, exists := message["betas"]; exists {
			t.Fatalf("normal caller default SDK beta must be undefined and omitted, got %s", message["betas"])
		}
		if string(message["session_id"]) != `"`+args.SessionID+`"` || string(message["claude_code_version"]) != `"fixture"` {
			t.Fatal("caller init lost actual session/version bindings", string(line))
		}
	}
	if inits != 1 {
		t.Fatalf("want one real init, got %d: %s", inits, out.Bytes())
	}
}
