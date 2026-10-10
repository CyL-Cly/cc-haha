package providernative

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkstream"
)

type originalConsumerInput struct {
	ID                string          `json:"id"`
	Request           json.RawMessage `json:"request"`
	Prefix            string          `json:"prefix"`
	Tail              string          `json:"tail"`
	ReturnValue       string          `json:"returnValue"`
	CallerAbortReason string          `json:"callerAbortReason"`
	EndResponseOnTail bool            `json:"endResponseOnTail"`
}
type consumerObservation struct {
	ID            string               `json:"id"`
	Values        []json.RawMessage    `json:"values"`
	Settlements   []consumerSettlement `json:"settlements"`
	CallerAborted bool                 `json:"callerAborted"`
	SDKAborted    bool                 `json:"sdkAborted"`
}
type consumerSettlement struct {
	Label string          `json:"label"`
	Value json.RawMessage `json:"value"`
}

// Uses only the five frozen original request/response/control programs. Source
// observations are compared after these runs; they are never runtime inputs.
func TestNativeOriginalConsumerPrograms(t *testing.T) {
	input, err := os.ReadFile("testdata/original-consumer-programs.json")
	if err != nil {
		t.Fatal(err)
	}
	var profiles []originalConsumerInput
	if err = json.Unmarshal(input, &profiles); err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		t.Run(profile.ID, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			tail := make(chan struct{})
			peerDone := make(chan error, 1)
			go func() {
				peer, err := listener.Accept()
				if err != nil {
					peerDone <- err
					return
				}
				defer peer.Close()
				_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
				request, err := httpReadRequest(peer)
				if err != nil {
					peerDone <- err
					return
				}
				compact := json.RawMessage{}
				var buffer bytes.Buffer
				if err = json.Compact(&buffer, profile.Request); err != nil {
					peerDone <- err
					return
				}
				compact = buffer.Bytes()
				if string(request.body) != string(compact) || request.path != "/v1/messages" {
					peerDone <- errors.New("original independent prepared request changed")
					return
				}
				if _, err = io.WriteString(peer, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n"); err != nil {
					peerDone <- err
					return
				}
				if _, err = fmt.Fprintf(peer, "%x\r\n%s\r\n", len(profile.Prefix), profile.Prefix); err != nil {
					peerDone <- err
					return
				}
				if profile.ID == "normal-tail-control" || profile.ID == "return-queued-after-next" {
					<-tail
					if _, err = fmt.Fprintf(peer, "%x\r\n%s\r\n", len(profile.Tail), profile.Tail); err != nil {
						peerDone <- err
						return
					}
					if profile.EndResponseOnTail {
						if _, err = io.WriteString(peer, "0\r\n\r\n"); err != nil {
							peerDone <- err
							return
						}
					}
				}
				rest, err := io.ReadAll(peer)
				if err == nil && len(rest) > 0 {
					err = errors.New("second physical request")
				}
				peerDone <- err
			}()
			owner := cooperative.New()
			defer owner.Close()
			caller, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			observation := consumerObservation{ID: profile.ID, Values: []json.RawMessage{}, Settlements: []consumerSettlement{}}
			err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				config, err := bodyConfig(owner, scope, "http://"+listener.Addr().String(), nil)
				if err != nil {
					return err
				}
				var buffer bytes.Buffer
				if err = json.Compact(&buffer, profile.Request); err != nil {
					return err
				}
				raw, err := provider.NewAnthropic(config).OpenMessagesStream(caller, &provider.MessagesRequest{Body: buffer.Bytes(), Path: "/v1/messages"})
				if err != nil {
					return err
				}
				defer raw.Close()
				owned, err := provider.NewOwnedMessageStream(scope, owner, raw)
				if err != nil {
					return err
				}
				settle := func(label string, future *ownerasync.OwnerFuture[ownerasync.IteratorResult[*sdkstream.Value]]) (ownerasync.IteratorResult[*sdkstream.Value], error) {
					result, err := future.Await(scope)
					if err != nil {
						return result, err
					}
					object := map[string]any{"done": result.Done}
					if !result.Done || result.Defined {
						object["value"] = result.Value
					}
					encoded, err := json.Marshal(object)
					if err != nil {
						return result, err
					}
					observation.Settlements = append(observation.Settlements, consumerSettlement{label, encoded})
					if !result.Done {
						value, err := result.Value.MarshalJSON()
						if err != nil {
							return result, err
						}
						observation.Values = append(observation.Values, value)
					}
					return result, nil
				}
				first, err := owned.Iterator.Next(scope)
				if err != nil {
					return err
				}
				if _, err = settle("first-next", first); err != nil {
					return err
				}
				switch profile.ID {
				case "normal-tail-control":
					close(tail)
					for nextCalls := 2; ; nextCalls++ {
						next, err := owned.Iterator.Next(scope)
						if err != nil {
							return err
						}
						result, err := settle(fmt.Sprintf("next-%d", nextCalls), next)
						if err != nil {
							return err
						}
						if result.Done {
							break
						}
					}
				case "return-at-yield":
					encoded, _ := json.Marshal(profile.ReturnValue)
					value, err := sdkstream.ParseValue(encoded)
					if err != nil {
						return err
					}
					returned, err := owned.Iterator.Return(scope, value)
					if err != nil {
						return err
					}
					if _, err = settle("return", returned); err != nil {
						return err
					}
				case "return-queued-after-next":
					next, err := owned.Iterator.Next(scope)
					if err != nil {
						return err
					}
					encoded, _ := json.Marshal(profile.ReturnValue)
					value, err := sdkstream.ParseValue(encoded)
					if err != nil {
						return err
					}
					returned, err := owned.Iterator.Return(scope, value)
					if err != nil {
						return err
					}
					close(tail)
					if _, err = settle("pending-next", next); err != nil {
						return err
					}
					if _, err = settle("return", returned); err != nil {
						return err
					}
				case "caller-abort-after-next", "cleanup-resource-after-next":
					next, err := owned.Iterator.Next(scope)
					if err != nil {
						return err
					}
					if profile.ID == "caller-abort-after-next" {
						cancel(errors.New(profile.CallerAbortReason))
					} else {
						if err = owned.AbortSDK(scope); err != nil {
							return err
						}
					}
					if _, err = settle("pending-next", next); err != nil {
						return err
					}
				default:
					return errors.New("unrecognized original consumer program")
				}
				observation.CallerAborted = caller.Err() != nil
				observation.SDKAborted, err = owned.SDKAborted(scope)
				if err != nil {
					return err
				}
				if profile.ID != "caller-abort-after-next" && raw.AbortCause() != nil {
					return fmt.Errorf("SDK resource stop fabricated caller cause: %v", raw.AbortCause())
				}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
			joinedPeer(t, peerDone)
			encoded, err := json.Marshal(observation)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("original-consumer-observation %s", encoded)
		})
	}
}

type physicalRequest struct {
	body []byte
	path string
}

func httpReadRequest(conn net.Conn) (physicalRequest, error) {
	request, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return physicalRequest{}, err
	}
	body, err := io.ReadAll(request.Body)
	return physicalRequest{body, request.URL.Path}, err
}
