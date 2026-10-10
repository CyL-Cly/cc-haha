package provider

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This is the production lease join contract, with a real pipe establishing
// the read/completion order. No SDK phase or expected message drives the job.
func TestDemandReadCompletionAfterOwnerCloseDoesNotJoinItself(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	entered := make(chan struct{})
	physicalClosed := make(chan struct{})
	var announced sync.Once
	driver := &OwnerOpenIO{leases: map[uint64]*ownerLease{}}
	lease := &ownerLease{id: 1, driver: driver, body: &readStartObserver{ReadCloser: reader, entered: entered}, cancel: func() { announced.Do(func() { close(physicalClosed) }) }}
	driver.leases[1] = lease
	owner := cooperative.New()
	body := &physicalDemandBody{owner: owner, ctx: context.Background(), lease: lease, readLease: lease}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := body.Next(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	<-entered
	owner.Close()
	if _, err := writer.Write([]byte("original physical read completion")); err != nil {
		t.Fatal(err)
	}
	<-physicalClosed
	joined := make(chan struct{})
	go func() { _ = lease.Close(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("discarded read completion attempted Close before ending its own admitted lease read")
	}
	driver.mu.Lock()
	remaining := len(driver.leases)
	driver.mu.Unlock()
	if remaining != 0 {
		t.Fatal("unadopted physical lease remains registered", remaining)
	}
}

type demandedCloseReader struct {
	io.Reader
	closed  int
	failure error
}

func (reader *demandedCloseReader) Close() error { reader.closed++; return reader.failure }

func TestOwnedStreamClaimReturnAndOriginalCloseFailure(t *testing.T) {
	for _, failure := range []error{nil, errors.New("original physical return close error")} {
		owner := cooperative.New()
		foreign := cooperative.New()
		lease := &demandedCloseReader{Reader: strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"), failure: failure}
		ctx, cancel := context.WithCancel(context.Background())
		raw := &RawMessageStream{ctx: ctx, cancel: cancel, body: lease, decoder: protocol.NewSDKSSEDecoder(nil)}
		err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			if _, err := NewOwnedMessageStream(scope, foreign, raw); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("foreign owner adopted original lease", err)
			}
			if _, err := NewOwnedMessageStream(scope, owner, nil); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("nil stream adopted", err)
			}
			stream, err := NewOwnedMessageStream(scope, owner, raw)
			if err != nil {
				return err
			}
			if _, err := NewOwnedMessageStream(scope, owner, raw); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("second consumer adopted response", err)
			}
			if _, err := raw.Next(); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal("legacy Next consumed claimed response", err)
			}
			next, err := stream.Iterator.Next(scope)
			if err != nil {
				return err
			}
			value, err := next.Await(scope)
			if err != nil || value.Done || value.Value.Object == nil {
				t.Fatal(value, err)
			}
			returned, err := stream.Iterator.ReturnUndefined(scope)
			if err != nil {
				return err
			}
			_, err = returned.Await(scope)
			if err != failure {
				t.Fatal("return changed original close rejection", err, failure)
			}
			return nil
		})
		owner.Close()
		foreign.Close()
		if err != nil || lease.closed != 1 || raw.AbortCause() != nil {
			t.Fatal(err, lease.closed, raw.AbortCause())
		}
	}
}
