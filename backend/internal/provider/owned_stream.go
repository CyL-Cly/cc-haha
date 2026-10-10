package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkstream"
)

// ParseSDKFrame is the existing SDK error/parser policy reused by the demand
// continuation. It operates only on the owner, never in a physical Read worker.
func ParseSDKFrame(frame protocol.SDKSSEEvent, headers *BunHeaders) (*sdkstream.Value, error) {
	if frame.Event == "error" {
		if headers != nil {
			return nil, NewSDKResponseError(nil, frame.Data, headers, false)
		}
		return nil, sdkAPIError(nil, frame.Data, nil, false)
	}
	if !json.Valid([]byte(frame.Data)) {
		return nil, sdkJSONSyntaxError(frame.Data)
	}
	return sdkstream.ParseValue(json.RawMessage(frame.Data))
}

type physicalDemandBody struct {
	owner      *cooperative.Owner
	ctx        context.Context
	lease      io.ReadCloser
	closeLease io.ReadCloser
	readLease  ownerReadLease
	native     bool
	busy       bool // owner-only; worker never accesses admission state
}

func (body *physicalDemandBody) Next(scope *cooperative.Scope) (*ownerasync.OwnerFuture[sdkstream.ReadResult], error) {
	if !scope.BelongsTo(body.owner) || body.busy {
		return nil, cooperative.ErrRestricted
	}
	future, err := ownerasync.NewOwnerFuture[sdkstream.ReadResult](body.owner)
	if err != nil {
		return nil, err
	}
	body.busy = true
	lease, resource, ctx, owner, native := body.lease, body.readLease, body.ctx, body.owner, body.native
	closing := body.closeLease
	if closing == nil {
		closing = lease
	}
	// Workers capture only the physical resource, cancellation and immutable result
	// obligation. All parser and Future resolution remains an owner completion.
	go func() {
		var result sdkstream.ReadResult
		if resource != nil && !resource.beginRead() {
			result.Failure = net.ErrClosed
		} else {
			scratch := make([]byte, 4096)
			count, failure := lease.Read(scratch)
			if native {
				failure = nativeBodyReadError(failure)
			}
			result = sdkstream.ReadResult{Bytes: append([]byte(nil), scratch[:count]...), Failure: failure}
			// The physical read has ended before completion admission. A rejected
			// admission closes/joins this lease and must not join its own read.
			if resource != nil {
				resource.endRead()
			}
		}
		if ctx.Err() != nil && !IsOwnerWaitFailure(result.Failure) {
			result.Failure = namedAbort{context.Cause(ctx)}
		}
		receipt, submitErr := owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			body.busy = false
			return future.Resolve(next, result)
		})
		if submitErr != nil {
			_ = closing.Close()
			return
		}
		// No observer is needed; the owner already owns completion. Administrative
		// close rejects the Future independently and native Close joins the Read.
		_ = receipt
	}()
	return future, nil
}
func (body *physicalDemandBody) Return(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	if !scope.BelongsTo(body.owner) || body.busy {
		return nil, cooperative.ErrRestricted
	}
	future, err := ownerasync.NewOwnerFuture[struct{}](body.owner)
	if err != nil {
		return nil, err
	}
	lease, owner := body.lease, body.owner
	if body.closeLease != nil {
		lease = body.closeLease
	}
	go func() {
		failure := lease.Close()
		_, _ = owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			if failure != nil {
				return future.Reject(next, failure)
			}
			return future.Resolve(next, struct{}{})
		})
	}()
	return future, nil
}

type namedAbort struct{ error }

func (failure namedAbort) Name() string { return "AbortError" }

// OwnedMessageStream has one claim on the original response, body and decoder.
// Next/Return are generator demands; Close is a distinct physical abort/join.
type OwnedMessageStream struct {
	Stream     *sdkstream.Stream
	Iterator   *ownerasync.OwnerIterator[*sdkstream.Value]
	raw        *RawMessageStream
	owner      *cooperative.Owner
	sdkContext context.Context
	sdkCancel  context.CancelCauseFunc
	sdkAborted bool // owner-only independent SDK controller
}

func NewOwnedMessageStream(scope *cooperative.Scope, owner *cooperative.Owner, raw *RawMessageStream) (*OwnedMessageStream, error) {
	if raw == nil || !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	raw.mu.Lock()
	if raw.closed || raw.owned {
		raw.mu.Unlock()
		return nil, cooperative.ErrRestricted
	}
	raw.mu.Unlock()
	lease := raw.body
	var readLease ownerReadLease
	native := false
	switch body := raw.body.(type) {
	case *nativeBodyReader:
		if _, err := body.driver.checked(body.lease); err != nil {
			return nil, err
		}
		lease, readLease = body.lease.physical, body.lease.physical
		native = true
	case *ownerBodyReader:
		lease, readLease = body.lease, body.lease
	}
	sdkContext, sdkCancel := context.WithCancelCause(raw.ctx)
	owned := &OwnedMessageStream{raw: raw, owner: owner, sdkContext: sdkContext, sdkCancel: sdkCancel}
	bodyContext := sdkContext
	if raw.openAI != nil && raw.openAI.bridge != nil {
		bodyContext = raw.openAI.bridge.Context()
	}
	physical := &physicalDemandBody{owner: owner, ctx: bodyContext, lease: lease, readLease: readLease, native: native}
	var body sdkstream.Body = physical
	if raw.openAI != nil {
		physical.closeLease = raw.body
		mapped := newOwnedOpenAIBody(owner, physical, raw.openAI)
		if err := mapped.start(scope); err != nil {
			return nil, err
		}
		body = mapped
	}
	stream, err := sdkstream.New(owner, body, raw.decoder, func(frame protocol.SDKSSEEvent) (*sdkstream.Value, error) {
		return ParseSDKFrame(frame, raw.OriginalHeaders)
	}, func(next *cooperative.Scope) {
		// Capture the caller cause before canceling the SDK resource context;
		// cancellation of the latter must not create a caller abort identity.
		raw.mu.Lock()
		if raw.ctx.Err() != nil {
			raw.aborted = context.Cause(raw.ctx)
		}
		raw.mu.Unlock()
		_ = owned.AbortSDK(next)
	}, raw.diagnose)
	if err != nil {
		return nil, err
	}
	iterator, err := stream.Iterator()
	if err != nil {
		return nil, err
	}
	raw.mu.Lock()
	raw.owned = true
	raw.mu.Unlock()
	owned.Stream, owned.Iterator = stream, iterator
	return owned, nil
}

// AbortSDK is Source cleanupStream's resource controller. It never cancels the
// original caller; its actual canceled read is an SDK AbortError, not a blanket
// conversion of transport/parser faults into EOF.
func (stream *OwnedMessageStream) AbortSDK(scope *cooperative.Scope) error {
	if stream == nil || !scope.BelongsTo(stream.owner) {
		return cooperative.ErrRestricted
	}
	stream.sdkAborted = true
	stream.sdkCancel(namedAbort{errors.New("The operation was aborted.")})
	stream.raw.cancel()
	return nil
}
func (stream *OwnedMessageStream) SDKAborted(scope *cooperative.Scope) (bool, error) {
	if stream == nil || !scope.BelongsTo(stream.owner) {
		return false, cooperative.ErrRestricted
	}
	return stream.sdkAborted || stream.raw.ctx.Err() != nil, nil
}
func (stream *OwnedMessageStream) Close() error { return stream.raw.Close() }

// Physical cancellation is an SDK AbortError only when the actual caller or
// independent SDK resource context aborted. Administrative owner failures retain their marker.
func StreamDemandFailure(failure error, administrative bool) error {
	if administrative || errors.Is(failure, cooperative.ErrClosed) || errors.Is(failure, cooperative.ErrRestricted) {
		return &ownerWaitFailure{failure}
	}
	return failure
}
