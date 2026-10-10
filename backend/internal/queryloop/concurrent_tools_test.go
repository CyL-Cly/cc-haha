package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestReadBatchStartsNativeIOConcurrentlyAndDefersModifiersInInvocationOrder(t *testing.T) {
	h := newBatchHarness(t)
	owner := cooperative.New()
	defer owner.Close()
	dir := t.TempDir()
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	firstStarted, releaseFirst := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(releaseFirst) })
	h.Tool.Name = "Read"
	h.Tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return true }
	h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
		return ToolCallResult{}, errors.New("synchronous call selected for parallel IO")
	}
	h.Tool.StartCallJSON = func(_ context.Context, in protocol.JSONObject, _ JSONToolCallContext) (*ownerasync.OwnerFuture[ToolCallResult], error) {
		name := in.String("file_path")
		future, err := ownerasync.NewOwnerFuture[ToolCallResult](owner)
		if err != nil {
			return nil, err
		}
		go func() {
			if name == "first" {
				close(firstStarted)
				<-releaseFirst
			} else {
				<-firstStarted
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			receipt, posted := owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
				if err != nil {
					return future.Reject(scope, err)
				}
				return future.Resolve(scope, ToolCallResult{Data: quoted(string(data)), ModifyContext: func(c any) (any, error) { return c.(string) + name, nil }})
			})
			if posted == nil {
				posted = receipt.Wait()
			}
			if posted != nil {
				_ = future.RejectAdministrative(posted)
			}
		}()
		return future, nil
	}
	write := h.Tool
	write.Name = "Write"
	write.StartCallJSON = nil
	write.IsConcurrencySafe = func(protocol.JSONObject) bool { return false }
	write.Call = func(_ context.Context, _ protocol.JSONObject, c ToolCallContext) (ToolCallResult, error) {
		if c.Opaque != "initialfirstsecond" {
			return ToolCallResult{}, fmt.Errorf("write saw context %v before invocation-ordered modifiers", c.Opaque)
		}
		return ToolCallResult{Data: quoted("written")}, nil
	}
	uses := []Invocation{}
	for _, name := range []string{"first", "second", "write"} {
		use := h.Uses[0]
		block := *use.Block
		block.Object = block.Object.Clone()
		use.Block = &block
		toolName := "Read"
		if name == "write" {
			toolName = "Write"
		}
		block.Object.Set("id", quoted(name))
		block.Object.Set("name", quoted(toolName))
		block.Object.Set("input", []byte(`{"file_path":"`+name+`","content":"x"}`))
		uses = append(uses, use)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	seen := []string{}
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		h.Capabilities.Owner, h.Capabilities.Scope, h.Capabilities.MaxConcurrency = owner, scope, 2
		batch, err := NewToolBatch([]RegisteredTool{h.Tool, write}, h.Capabilities, h.Factory)
		if err != nil {
			return err
		}
		_, err = batch.Execute(ctx, Round{ToolContext: "initial"}, uses, func(m Message) error {
			var value string
			if err := json.Unmarshal(m.Record.Get("toolUseResult"), &value); err != nil {
				return err
			}
			seen = append(seen, value)
			if value == "second" {
				release.Do(func() { close(releaseFirst) })
			}
			return nil
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []string{"second", "first", "written"}) {
		t.Fatalf("completion order %v", seen)
	}
}

func TestReadBatchMissingAsyncCapabilityDoesNotSerializeOrCallTools(t *testing.T) {
	h := newBatchHarness(t)
	h.Tool.Name = "Read"
	h.Tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return true }
	h.Uses[0].Block.Object.Set("name", quoted("Read"))
	h.Uses = append(h.Uses, h.Uses[0])
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		h.Capabilities.Owner, h.Capabilities.Scope = owner, scope
		batch, err := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
		if err != nil {
			return err
		}
		_, err = batch.Execute(context.Background(), Round{}, h.Uses, h.Emit)
		return err
	})
	if err == nil || h.Calls != 0 || h.UUIDs != 0 {
		t.Fatalf("missing native path accepted %v calls=%d messages=%d", err, h.Calls, h.UUIDs)
	}
}
