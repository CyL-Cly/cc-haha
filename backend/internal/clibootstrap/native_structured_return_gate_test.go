package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestActualEndSessionHeldRawReturnCompletion(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "fulfill", true: "reject-identity"}[reject], func(t *testing.T) {
			m := taskMainFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			gate, err := sdkhost.NewOwnerFuture[struct{}](m.Owner)
			if err != nil {
				t.Fatal(err)
			}
			closeFailure := errors.New("actual externally held raw return failure")
			acknowledged, closeEntered := make(chan struct{}), make(chan struct{})
			finished := false // accessed only on this Owner, sampled before resolving gate
			acks, finallyEntered, finallyCompleted := 0, 0, 0
			m.options.OnOutput = func(data []byte) error {
				var frame, response protocol.JSONObject
				if err := json.Unmarshal(data, &frame); err != nil {
					return err
				}
				if frame.String("type") != "control_response" {
					return fmt.Errorf("unexpected no-model output: %s", data)
				}
				if err := json.Unmarshal(frame.Get("response"), &response); err != nil {
					return err
				}
				if response.String("request_id") != "held-end" || response.String("subtype") != "success" {
					return fmt.Errorf("wrong actual end_session ACK: %s", data)
				}
				acks++
				if acks == 1 {
					close(acknowledged)
				}
				return nil
			}
			done := make(chan error, 1)
			go func() {
				done <- m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					raw, err := sdkhost.NewOwnerIterator(m.Owner, func(next *cooperative.Scope, it *sdkhost.OwnerIterator[string], _ sdkhost.IteratorRequest[string]) error {
						return it.Yield(next, inboxControl("held-end", map[string]any{"subtype": "end_session"}), func(entered *cooperative.Scope, it *sdkhost.OwnerIterator[string], request sdkhost.IteratorRequest[string]) error {
							if !request.Returning {
								return errors.New("actual raw input resumed instead of Return")
							}
							finallyEntered++
							if finallyEntered == 1 {
								close(closeEntered)
							}
							// This is the producer's own unresolved async finally work.
							// No phase observer or unrelated I/O advances the consumer.
							return sdkhost.AwaitGenerator(entered, it, gate, func(released *cooperative.Scope, _ struct{}) error {
								finallyCompleted++
								return it.CompleteReturn(released)
							}, nil)
						})
					})
					if err != nil {
						return err
					}
					input, err := sdkhost.NewPlainStructuredInputWithDiagnostics(m.Owner, raw, m.diagnostics)
					if err != nil {
						return err
					}
					err = m.Host.RunStream(scope, ctx, input, sdkhost.StreamOptions{})
					finished = true
					return err
				})
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					<-done
				}
			}()
			wait := func(signal <-chan struct{}, label string) {
				select {
				case <-signal:
				case err := <-done:
					joined = true
					t.Fatalf("RunStream completed before %s, raw Return gate still held: %v", label, err)
				case <-ctx.Done():
					t.Fatalf("actual %s did not arrive: %v", label, ctx.Err())
				}
			}
			wait(acknowledged, "ACK")
			wait(closeEntered, "raw finally admission")
			// This callback is the external completion of the already admitted raw
			// producer job. Checking the state and releasing/rejecting its promise
			// on one Owner callback prevents a separate off-owner sampling race.
			receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
				select {
				case <-gate.Done():
					return errors.New("raw close gate settled before external completion")
				default:
				}
				if finished || acks != 1 || finallyEntered != 1 || finallyCompleted != 0 {
					return fmt.Errorf("held actual return state finished=%t ACKs=%d finally=%d/%d", finished, acks, finallyEntered, finallyCompleted)
				}
				if reject {
					return gate.Reject(scope, closeFailure)
				}
				return gate.Resolve(scope, struct{}{})
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = receipt.Wait(); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				joined = true
			case <-ctx.Done():
				t.Fatal("RunStream did not finish after actual raw completion", ctx.Err())
			}
			if reject {
				if err != closeFailure {
					t.Fatalf("held close error identity lost: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantComplete := 1
			if reject {
				wantComplete = 0
			}
			if !finished || acks != 1 || finallyEntered != 1 || finallyCompleted != wantComplete {
				t.Fatalf("actual return lifecycle finished=%t ACKs=%d finally=%d/%d", finished, acks, finallyEntered, finallyCompleted)
			}
			if len(m.Controller.History()) != 0 {
				t.Fatal("held end_session started a user turn")
			}
		})
	}
}
