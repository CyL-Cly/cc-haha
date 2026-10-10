package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

type operation struct {
	Op         string      `json:"op"`
	Fn         string      `json:"fn,omitempty"`
	Handle     string      `json:"handle,omitempty"`
	Run        string      `json:"run,omitempty"`
	Call       int         `json:"call,omitempty"`
	Outcome    string      `json:"outcome,omitempty"`
	Error      string      `json:"error,omitempty"`
	Operations []operation `json:"operations,omitempty"`
}
type definition struct {
	ID      string      `json:"id"`
	Returns string      `json:"returns"`
	Error   string      `json:"error,omitempty"`
	OnCall  []operation `json:"onCall,omitempty"`
}
type profile struct {
	ID         string       `json:"id"`
	Functions  []definition `json:"functions"`
	Operations []operation  `json:"operations"`
}
type heldEntry struct {
	future  *runtimecleanup.Future
	settled bool
}
type fixture struct {
	owner      *cooperative.Owner
	registry   *runtimecleanup.Registry
	trace      []map[string]any
	functions  map[string]*runtimecleanup.Callback
	handles    map[string]runtimecleanup.Unregister
	counts     map[string]int
	runs       map[string]*runtimecleanup.Future
	runStates  map[string]string
	held       map[string]*heldEntry
	microtasks int
}

func (fixture *fixture) push(event string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["event"] = event
	fixture.trace = append(fixture.trace, data)
}
func reason(err error) map[string]string {
	return map[string]string{"name": "Error", "message": err.Error()}
}
func originalError(text, fallback string) error {
	if text == "" {
		text = fallback
	}
	return errors.New(text)
}
func heldKey(fn string, call int) string { return fmt.Sprintf("%s:%d", fn, call) }

func (fixture *fixture) apply(scope *cooperative.Scope, input operation) error {
	switch input.Op {
	case "register":
		callback := fixture.functions[input.Fn]
		if callback == nil {
			return errors.New("unknown original callback")
		}
		remove, err := fixture.registry.Register(scope, callback)
		if err != nil {
			return err
		}
		fixture.handles[input.Handle] = remove
		fixture.push("register", map[string]any{"fn": input.Fn, "handle": input.Handle})
	case "unregister":
		remove := fixture.handles[input.Handle]
		if remove == nil {
			return errors.New("missing original unregister handle")
		}
		deleted, err := remove(scope)
		if err != nil {
			return err
		}
		fixture.push("unregister", map[string]any{"handle": input.Handle, "deleted": deleted})
	case "run":
		if fixture.runs[input.Run] != nil {
			return errors.New("duplicate original run ID")
		}
		fixture.push("run:enter", map[string]any{"run": input.Run})
		future, err := fixture.registry.Start(scope)
		if err != nil {
			return err
		}
		fixture.runs[input.Run], fixture.runStates[input.Run] = future, "pending"
		fixture.push("run:return", map[string]any{"run": input.Run, "returnedPromise": true})
		_, err = future.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				fixture.runStates[input.Run] = "rejected"
				fixture.push("run:rejected", map[string]any{"run": input.Run, "error": reason(failure)})
			} else {
				fixture.runStates[input.Run] = "fulfilled"
				fixture.push("run:fulfilled", map[string]any{"run": input.Run})
			}
			return nil
		})
		return err
	case "settle":
		entry := fixture.held[heldKey(input.Fn, input.Call)]
		if entry == nil || entry.settled {
			return errors.New("invalid original held settlement")
		}
		entry.settled = true
		fixture.push("held:settle", map[string]any{"fn": input.Fn, "call": input.Call, "outcome": input.Outcome})
		if input.Outcome == "reject" {
			return entry.future.Reject(scope, originalError(input.Error, "original-held-rejection"))
		}
		if input.Outcome != "resolve" {
			return errors.New("invalid held outcome")
		}
		return entry.future.Resolve(scope, struct{}{})
	case "microtask":
		fixture.microtasks++
		id := fixture.microtasks
		fixture.push("microtask:scheduled", map[string]any{"id": id})
		_, err := fixture.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
			fixture.push("microtask:enter", map[string]any{"id": id})
			for _, command := range input.Operations {
				if err := fixture.apply(next, command); err != nil {
					return err
				}
			}
			fixture.push("microtask:leave", map[string]any{"id": id})
			return nil
		})
		return err
	default:
		return errors.New("async or unsupported operation reached synchronous callback")
	}
	return nil
}

func (fixture *fixture) define(input definition) error {
	if fixture.functions[input.ID] != nil {
		return errors.New("duplicate original callback")
	}
	switch input.Returns {
	case "undefined", "resolved", "rejected", "throw", "held":
	default:
		return errors.New("unsupported original callback return")
	}
	fixture.functions[input.ID] = runtimecleanup.NewCallback(func(scope *cooperative.Scope) (*runtimecleanup.Future, error) {
		fixture.counts[input.ID]++
		call := fixture.counts[input.ID]
		fixture.push("callback:start", map[string]any{"fn": input.ID, "call": call})
		for _, command := range input.OnCall {
			if err := fixture.apply(scope, command); err != nil {
				return nil, err
			}
		}
		if input.Returns == "throw" {
			failure := originalError(input.Error, "original-sync-throw")
			fixture.push("callback:throw", map[string]any{"fn": input.ID, "call": call, "error": reason(failure)})
			return nil, failure
		}
		var future *runtimecleanup.Future
		if input.Returns != "undefined" {
			future, _ = ownerasync.NewOwnerFuture[struct{}](fixture.owner)
			switch input.Returns {
			case "resolved":
				_ = future.Resolve(scope, struct{}{})
			case "rejected":
				_ = future.Reject(scope, originalError(input.Error, "original-rejection"))
			case "held":
				fixture.held[heldKey(input.ID, call)] = &heldEntry{future: future}
			}
		}
		fixture.push("callback:return", map[string]any{"fn": input.ID, "call": call, "kind": input.Returns})
		return future, nil
	})
	return nil
}

func (fixture *fixture) execute(scope *cooperative.Scope, commands []operation) error {
	for _, command := range commands {
		switch command.Op {
		case "await":
			future := fixture.runs[command.Run]
			if future == nil {
				return errors.New("missing original run")
			}
			fixture.push("await:enter", map[string]any{"run": command.Run})
			if _, err := future.Await(scope); err != nil {
				fixture.push("await:rejected", map[string]any{"run": command.Run, "error": reason(err)})
			} else {
				fixture.push("await:fulfilled", map[string]any{"run": command.Run})
			}
		case "awaitHeld":
			entry := fixture.held[heldKey(command.Fn, command.Call)]
			if entry == nil {
				return errors.New("missing original held invocation")
			}
			fixture.push("await-held:enter", map[string]any{"fn": command.Fn, "call": command.Call})
			if _, err := entry.future.Await(scope); err != nil {
				fixture.push("await-held:rejected", map[string]any{"fn": command.Fn, "call": command.Call, "error": reason(err)})
			} else {
				fixture.push("await-held:fulfilled", map[string]any{"fn": command.Fn, "call": command.Call})
			}
		default:
			if err := fixture.apply(scope, command); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func execute() error {
	home := os.Getenv("HOME")
	if err := assertHome(home); err != nil {
		return err
	}
	inputPath := filepath.Join(home, "input.json")
	if err := guard(home, inputPath, true); err != nil {
		return err
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}
	var original profile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&original); err != nil {
		return err
	}
	owner := cooperative.New()
	defer owner.Close()
	registry, _ := runtimecleanup.New(owner)
	fixture := &fixture{owner: owner, registry: registry, trace: []map[string]any{}, functions: map[string]*runtimecleanup.Callback{},
		handles: map[string]runtimecleanup.Unregister{}, counts: map[string]int{}, runs: map[string]*runtimecleanup.Future{}, runStates: map[string]string{}, held: map[string]*heldEntry{}}
	for _, definition := range original.Functions {
		if err := fixture.define(definition); err != nil {
			return err
		}
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error { return fixture.execute(scope, original.Operations) }); err != nil {
		return err
	}
	heldStates := map[string]map[string]bool{}
	for key, entry := range fixture.held {
		heldStates[key] = map[string]bool{"settled": entry.settled}
	}
	result := struct {
		Original   json.RawMessage            `json:"original"`
		Trace      []map[string]any           `json:"trace"`
		Counts     map[string]int             `json:"counts"`
		RunStates  map[string]string          `json:"runStates"`
		HeldStates map[string]map[string]bool `json:"heldStates"`
	}{json.RawMessage(raw), fixture.trace, fixture.counts, fixture.runStates, heldStates}
	return json.NewEncoder(os.Stdout).Encode(result)
}
