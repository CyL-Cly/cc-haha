package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
)

func TestNativeCommandPluginEventsPreserveActualHandlerScopesAndIdentifierParsing(t *testing.T) {
	f := newNativePluginFixture(t)
	m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	got := []string{}
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		capture := func(_ *cooperative.Scope, event analytics.Event) error {
			body, err := event.Metadata.MarshalJSON()
			got = append(got, event.Name+":"+string(body))
			return err
		}
		if err := m.commandEvents.Attach(scope, &analytics.PublicSink{Log: capture, LogAsync: func(next *cooperative.Scope, event analytics.Event) (*ownerasync.OwnerFuture[struct{}], error) {
			pending, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
			if err != nil {
				return nil, err
			}
			if err := capture(next, event); err != nil {
				return nil, err
			}
			return pending, pending.Resolve(next, struct{}{})
		}}); err != nil {
			return err
		}
		for _, command := range []plugincli.Command{{Action: "list"}, {Action: "install", Argument: "sample@mkt@ignored"}, {Action: "uninstall", Argument: "sample", Scope: "project"}, {Action: "enable", Argument: "sample@mkt"}, {Action: "enable", Argument: "sample@mkt", Cowork: true}, {Action: "disable", Argument: "sample@mkt", Cowork: true}, {Action: "disable", All: true}, {Action: "update", Argument: "sample@mkt", Scope: "invalid"}} {
			if err := m.nativeCommandPluginEvent(scope, command); err != nil {
				return err
			}
		}
		// The update handler emits before scope validation; install emits only
		// after validation. Exercise the actual handler's preflight boundary.
		before := len(got)
		result, err := m.PluginCommand(context.Background(), scope, plugincli.Command{Action: "install", Argument: "sample@mkt", Scope: "invalid"})
		if err != nil || result.ExitCode != 1 || len(got) != before {
			return fmt.Errorf("invalid install emitted before actual scope validation: %v %+v %v", err, result, got)
		}
		result, err = m.PluginCommand(context.Background(), scope, plugincli.Command{Action: "update", Argument: "sample@mkt", Scope: "invalid"})
		if err != nil || result.ExitCode != 1 || len(got) != before+1 {
			return fmt.Errorf("invalid update lost its actual pre-validation event: %v %+v %v", err, result, got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`tengu_plugin_list_command:{}`,
		`tengu_plugin_install_command:{"_PROTO_plugin_name":"sample","_PROTO_marketplace_name":"mkt","scope":"user"}`,
		`tengu_plugin_uninstall_command:{"_PROTO_plugin_name":"sample","scope":"project"}`,
		`tengu_plugin_enable_command:{"_PROTO_plugin_name":"sample","_PROTO_marketplace_name":"mkt","scope":"auto"}`,
		`tengu_plugin_enable_command:{"_PROTO_plugin_name":"sample","_PROTO_marketplace_name":"mkt","scope":"user"}`,
		`tengu_plugin_disable_command:{"_PROTO_plugin_name":"sample","_PROTO_marketplace_name":"mkt","scope":"user"}`,
		`tengu_plugin_disable_command:{}`,
		`tengu_plugin_update_command:{"_PROTO_plugin_name":"sample","_PROTO_marketplace_name":"mkt"}`,
		`tengu_plugin_update_command:{"_PROTO_plugin_name":"sample","_PROTO_marketplace_name":"mkt"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("actual Source command producer metadata changed", got)
	}
}
