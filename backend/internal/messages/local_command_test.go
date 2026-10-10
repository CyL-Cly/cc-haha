package messages

import (
	"testing"
	"time"
)

func TestLocalCommandFactoryKeepsOriginalClockUUIDOrderAndCaveat(t *testing.T) {
	calls := []string{}
	f, _ := NewFactory(func() string { calls = append(calls, "uuid"); return "actual-id" }, func() time.Time { calls = append(calls, "time"); return time.UnixMilli(1700000000000) })
	local := f.CreateLocalCommandInput("<local-command-stdout>original business result</local-command-stdout>")
	if len(calls) != 2 || calls[0] != "time" || calls[1] != "uuid" || local.String("uuid") != "actual-id" || local.String("timestamp") != "2023-11-14T22:13:20.000Z" || local.String("subtype") != "local_command" || string(local.Get("isMeta")) != "false" {
		t.Fatal(calls, local)
	}
	caveat := f.CreateLocalCaveat()
	if string(caveat.Get("isMeta")) != "true" {
		t.Fatal("caveat should not be replayed as authored user")
	}
}
