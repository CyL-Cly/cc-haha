package sdkhost

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func chunkIterator(owner *cooperative.Owner, chunks []string) (*OwnerIterator[string], error) {
	index := 0
	var step GeneratorContinuation[string]
	step = func(scope *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
		if request.Returning {
			return iterator.CompleteReturn(scope)
		}
		if index == len(chunks) {
			return iterator.Complete(scope)
		}
		value := chunks[index]
		index++
		return iterator.Yield(scope, value, step)
	}
	return NewOwnerIterator(owner, step)
}

func TestPlainStructuredInputActualDelegationRetainsPartialBlocksAndFinalLine(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		chunks := []string{"\n{\"type\":\"keep_alive\"}\n{\"type\":\"user\",\"uuid\":\"first\",", "\"message\":{\"role\":\"user\",\"content\":\"中文 😀\"}}\n\n{\"type\":\"user\",\"uuid\":\"second\",\"message\":{\"role\":\"user\",\"content\":\"last line\"}}"}
		raw, err := chunkIterator(owner, chunks)
		if err != nil {
			return err
		}
		input, err := NewPlainStructuredInput(owner, raw)
		if err != nil {
			return err
		}
		ids := []string{}
		for {
			pending, err := input.Next(scope)
			if err != nil {
				return err
			}
			result, err := pending.Await(scope)
			if err != nil {
				return err
			}
			if result.Done {
				break
			}
			ids = append(ids, result.Value.String("uuid"))
			if len(ids) == 1 && object(result.Value.Get("message")).String("content") != "中文 😀" {
				return errors.New("original partial block lost scalar content")
			}
		}
		if !reflect.DeepEqual(ids, []string{"first", "second"}) {
			return fmt.Errorf("original parsed input sequence %v", ids)
		}
		pending, err := input.Next(scope)
		if err != nil {
			return err
		}
		result, err := pending.Await(scope)
		if err != nil || !result.Done {
			return fmt.Errorf("completed read next %v/%v", result, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPlainStructuredInputEmptyStreamAndNonObjectBranches(t *testing.T) {
	for _, item := range []struct {
		name   string
		chunks []string
		cap    bool
	}{
		{"empty", nil, false},
		{"empty-lines", []string{"\n\n"}, false},
		{"ignored-keepalive", []string{"{\"type\":\"keep_alive\"}"}, false},
		{"parse-error", []string{"{bad\n"}, true},
		{"parse-error-final-EOF", []string{"{bad"}, true},
		{"non-object", []string{"[]\n"}, true},
		{"wrong-role", []string{"{\"type\":\"user\",\"message\":{\"role\":\"assistant\"}}\n"}, true},
		{"control", []string{"{\"type\":\"control_request\",\"request\":{}}\n"}, true},
		{"split-utf8", []string{string([]byte{0xf0, 0x9f})}, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				raw, err := chunkIterator(owner, item.chunks)
				if err != nil {
					return err
				}
				input, err := NewPlainStructuredInput(owner, raw)
				if err != nil {
					return err
				}
				next, err := input.Next(scope)
				if err != nil {
					return err
				}
				result, err := next.Await(scope)
				if item.cap {
					var failure *CapabilityError
					if !errors.As(err, &failure) {
						return fmt.Errorf("unsupported branch did not fail explicitly: %v/%v", result, err)
					}
					return nil
				}
				if err != nil || !result.Done || result.Defined {
					return fmt.Errorf("empty structured read: %v/%v", result, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPlainStructuredInputUsesCanonicalOwner(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	raw, err := chunkIterator(owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		owner *cooperative.Owner
		raw   *OwnerIterator[string]
	}{{nil, raw}, {owner, nil}, {other, raw}} {
		if _, err := NewPlainStructuredInput(item.owner, item.raw); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("different owner accepted", err)
		}
	}
	input, err := NewPlainStructuredInput(owner, raw)
	if err != nil || input.Iterator() == nil {
		t.Fatal("actual read iterator unavailable", err)
	}
	if err = other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := input.Next(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign read scope %v", err)
		}
		_, err := input.processLine(scope, "")
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign parse scope %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		pending, err := input.Iterator().Return(scope, protocol.NewJSONObject())
		if err != nil {
			return err
		}
		_, err = pending.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
