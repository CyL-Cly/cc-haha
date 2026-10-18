package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
)

// BindRemoteIO installs the concrete native CCR transcript sink on the same
// Project writer that assigns parent UUIDs and owns local persistence.
func (c *Controller) BindRemoteIO(scope *cooperative.Scope, remote *remoteio.Service) error {
	if remote == nil || c.options.Bootstrap == nil || !c.options.NativeTools || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	c.writer.mu.Lock()
	c.writer.remote = remote
	c.writer.mu.Unlock()
	if remote.Kind() == remoteio.CCR {
		c.commandLifecycle = func(id json.RawMessage, state string) {
			var value string
			if json.Unmarshal(id, &value) == nil {
				if state == "started" {
					state = "processing"
				} else if state == "completed" {
					state = "processed"
				}
				remote.ReportDelivery(value, state)
			}
		}
	}
	return nil
}

func (c *Controller) BindLegacyIngress(scope *cooperative.Scope, ingress *remoteio.Ingress, target string, life context.Context, enabled func() bool, onFailure ...func(error)) error {
	if ingress == nil || target == "" || life == nil || enabled == nil || c.options.Bootstrap == nil || !c.options.NativeTools || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	c.writer.mu.Lock()
	defer c.writer.mu.Unlock()
	c.writer.ingress, c.writer.ingressURL, c.writer.ingressContext, c.writer.ingressEnabled = ingress, target, life, enabled
	if len(onFailure) > 0 {
		c.writer.ingressFailure = onFailure[0]
	}
	return nil
}

func (job *appendContinuation) persistRemote(scope *cooperative.Scope) bool {
	writer := job.pending.writer
	writer.mu.Lock()
	remote := writer.remote
	ingress, target, life, enabled := writer.ingress, writer.ingressURL, writer.ingressContext, writer.ingressEnabled
	fatal := writer.ingressFailure
	ccr := remote != nil && remote.Kind() == remoteio.CCR
	legacy := ingress != nil && target != "" && enabled != nil && enabled()
	if !job.add || truthy(job.entry.Get("isSidechain")) && job.entry.Get("agentId") != nil {
		writer.mu.Unlock()
		return false
	}
	if kind := job.entry.String("type"); kind != "user" && kind != "assistant" && kind != "attachment" && kind != "system" {
		writer.mu.Unlock()
		return false
	}
	if !(ccr || legacy) {
		writer.mu.Unlock()
		// appendEntry awaits the async persistToRemote even when that method
		// returns immediately without a remote target. Only its fulfilled
		// Promise reaction remains; it starts no filesystem/network work and
		// does not extend enqueueWrite's already completed tracked counter.
		fulfilled := newWriteFuture()
		fulfilled.settle(nil)
		if err := fulfilled.thenOwner(job.pending.owner, func(*cooperative.Scope, error) { job.future.settle(nil) }, job.future.settle); err != nil {
			job.future.settle(err)
		}
		return true
	}
	encoded, err := writer.marshalLiveEntry(job.entry)
	writer.mu.Unlock()
	if err != nil {
		writer.options.OnError(err)
		return false
	}
	dispatchAppendOwned(job.pending, job.future, transcriptNativeUnwrapped, func() error {
		var payload protocol.JSONObject
		if err := payload.UnmarshalJSON(encoded); err != nil {
			return err
		}
		if ccr {
			return remote.WriteInternalEvent(context.Background(), "transcript", payload, payload.String("type") == "system" && payload.String("subtype") == "compact_boundary", payload.String("agentId"))
		}
		ok, err := ingress.Append(life, job.sid, target, payload)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("remote session persistence failed")
		}
		return nil
	}, nil, func(_ *cooperative.Scope, err error) {
		// Source catches a failed remote append, reports it diagnostically,
		// and preserves the already admitted local transcript/cache.
		if err != nil {
			writer.options.OnError(err)
			if !ccr {
				if fatal != nil && life.Err() == nil {
					fatal(err)
				}
				job.future.settle(err)
				return
			}
		}
		job.future.settle(nil)
	})
	return true
}
