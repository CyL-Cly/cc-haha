package sdkhost

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// StreamOptions allows an observational async-generator wrapper at the actual
// ASK surface. It receives original Go yields, never Source phases or prepared
// contexts. The original business fixture wraps ask through one real yield*.
type StreamOptions struct {
	WrapASK func(*OwnerIterator[[]byte]) (*OwnerIterator[[]byte], error)
}

type inputTask struct {
	host           *Host
	input          *PlainStructuredInput
	closed         bool
	failure        error
	wake           chan struct{}
	drainRequested bool
	cancelModel    context.CancelFunc
	interruptModel context.CancelCauseFunc
}

func (task *inputTask) notify() {
	if task.wake != nil {
		close(task.wake)
		task.wake = nil
	}
}

func (task *inputTask) next(scope *cooperative.Scope) error {
	next, err := task.input.Next(scope)
	if err != nil {
		return err
	}
	_, err = next.Then(scope, func(scope *cooperative.Scope, result IteratorResult[protocol.JSONObject], err error) error {
		if err != nil || result.Done {
			task.failure, task.closed = err, true
			task.notify()
			return nil
		}
		task.input.completeRemoteInlineLifecycle(result.Value)
		if result.Value.String("type") == "control_response" {
			_, e := task.host.enqueue(scope, result.Value)
			if e != nil {
				return e
			}
			task.notify()
			return task.next(scope)
		}
		if result.Value.String("type") == "control_request" {
			request := object(result.Value.Get("request"))
			switch request.String("subtype") {
			case "initialize":
				err = task.host.sdkHookInitializeControl(scope, result.Value)
			case "side_question", "cancel_side_question":
				_, err = task.host.sideQuestionControl(scope, result.Value)
			case "get_settings":
				err = task.host.sdkGetSettingsControl(scope, result.Value)
			case "mcp_status":
				err = task.host.sdkMCPStatusControl(scope, result.Value)
			case "mcp_message":
				err = task.host.sdkMCPMessageControl(scope, result.Value)
			case "mcp_set_servers", "mcp_reconnect", "mcp_toggle":
				var refresh bool
				refresh, err = task.host.sdkMCPReconcileControl(scope, result.Value)
				if err == nil {
					task.notify()
					// Admit the next input before the asynchronous SDK handshake.
					// Its control responses must advance on the same Owner.
					err = task.next(scope)
					if err == nil && refresh && task.host.options.NativeSDKPrepareTurn != nil {
						err = task.host.refreshSDKMCPAfterControl(scope)
					}
				}
				if err != nil {
					task.failure, task.closed = err, true
					task.notify()
				}
				return nil
			case "set_model", "set_max_thinking_tokens":
				err = task.host.sdkInferenceControl(scope, result.Value)
			case "set_permission_mode":
				if task.host.options.NativeSDKPermissionControl == nil {
					err = cooperative.ErrRestricted
				} else {
					err = task.host.options.NativeSDKPermissionControl(scope, result.Value, task.host.enqueueSDKControl)
				}
			case "get_context_usage":
				err = task.host.contextUsageControl(scope, result.Value)
			case "get_session_usage":
				err = task.host.sessionUsageControl(scope, result.Value)
			case "enqueue_session_message":
				var accepted bool
				accepted, err = task.host.sessionInboxControl(scope, result.Value)
				if err == nil && accepted && string(request.Get("start_if_idle")) == "true" {
					task.drainRequested = true
				}
			case "interrupt", "end_session":
				task.host.cancelSDKPromptSuggestion()
				err = task.host.sessionInboxStopControl(scope, result.Value, task.cancelModel)
				if request.String("subtype") == "end_session" && err == nil {
					// Source breaks its for-await only after the input iterator's
					// asynchronous return closes the active raw producer. The ACK
					// above precedes that close; ordinary EOF hooks do not run.
					returned, failure := task.input.Iterator().ReturnUndefined(scope)
					if failure == nil {
						_, failure = returned.Then(scope, func(_ *cooperative.Scope, _ IteratorResult[protocol.JSONObject], closeError error) error {
							task.failure, task.closed = closeError, true
							task.notify()
							return nil
						})
					}
					if failure != nil {
						task.failure, task.closed = failure, true
						task.notify()
					}
					return nil
				}
			case "send_agent_message":
				err = task.host.agentMessageControl(scope, result.Value)
			default:
				err = task.host.stopTaskControl(scope, result.Value)
			}
			if err != nil {
				task.failure, task.closed = err, true
				task.notify()
				return nil
			}
			task.notify()
			return task.next(scope)
		}
		_, err = task.host.serial.ContinueParsedSDK(scope, result.Value, func(scope *cooperative.Scope, admission sessioncontroller.Admission, err error) {
			if err == nil && admission.Acknowledgement != nil {
				var data []byte
				data, err = jsonBytes(*admission.Acknowledgement)
				if err == nil {
					_, err = task.host.deliverOutput(scope, data)
				}
			}
			if err == nil && admission.Historical {
				task.host.options.OnPhase("lifecycle:completed")
				if e := task.host.options.Controller.HistoricalCommandLifecycle(scope, result.Value.Get("uuid")); e != nil {
					err = e
				}
			}
			if err != nil {
				task.failure, task.closed = err, true
				task.notify()
				return
			}
			if !admission.Duplicate {
				task.drainRequested = true
				if result.Value.String("priority") == "now" && task.interruptModel != nil {
					task.interruptModel(queryloop.SubmitInterrupt)
				}
			}
			task.notify()
			if err = task.next(scope); err != nil {
				task.failure, task.closed = err, true
				task.notify()
			}
		})
		if err != nil {
			task.failure, task.closed = err, true
			task.notify()
		}
		return nil
	})
	return err
}

// RunStream retains one Frame-capable model stack. Input parsing/UUID/queue
// continuation belongs to the same session Owner and advances independently;
// only the existing drain dequeues, starts and consumes each ASK.
func (h *Host) RunStream(scope *cooperative.Scope, modelContext context.Context, input *PlainStructuredInput, options StreamOptions) error {
	if !scope.BelongsTo(h.options.Owner) || modelContext == nil || input == nil || input.owner != h.options.Owner || h.emitIterator != nil {
		return cap("stream input/canonical owner/model stack")
	}
	if h.closing {
		return cooperative.ErrClosed
	}
	if h.options.Output.Receipt != nil {
		return cap("active transport receipt/consumer return")
	}

	if input.diagnostics != h.options.Diagnostics {
		return cap("structured input diagnostics construction identity")
	}
	run, err := h.registerRunState(scope, input)
	if err != nil {
		return err
	}
	if h.options.Diagnostics != nil {
		if err := h.options.Diagnostics.Log("info", "cli_message_loop_started", nil); err != nil {
			return err
		}
	}
	task := &inputTask{host: h, input: input}
	input.environmentUpdate = h.options.NativeSDKEnvironmentUpdate
	input.allowStopTask = h.options.StopTask != nil
	input.allowAgentMessage = h.options.SendAgentMessage != nil
	input.allowSessionInbox = h.sessionInbox
	input.allowSessionUsage = h.options.Print != nil && h.options.Print.Write != nil && h.sessionAccounting != nil
	input.allowSideQuestion = h.sideQuestions != nil && h.nativeSideQuestionModels.Owns(h.options.Owner)
	input.allowContextUsage = h.options.Print != nil && h.options.Print.NativeContextUsage != nil
	if e := h.bindNativeSandboxInput(scope, input); e != nil {
		return e
	}
	if e := h.bindNativeSDKPermissionInput(scope, input); e != nil {
		return e
	}
	if e := h.bindNativeSDKHookInput(scope, input); e != nil {
		return e
	}
	if err := task.next(scope); err != nil {
		return err
	}
	runStarted := false
	for {
		if task.failure != nil {
			return task.failure
		}
		var initial *protocol.JSONObject
		var turnContext context.Context
		var interruptModel context.CancelCauseFunc
		var cancelModel context.CancelFunc
		if run != nil && run.active && !task.drainRequested {
			queued, e := h.options.Controller.HasMainThreadQueuedCommandOnOwner(scope)
			if e != nil {
				return e
			}
			if queued {
				task.drainRequested = true
			}
		}
		if task.drainRequested {
			if !runStarted {
				if err := run.begin(scope); err != nil {
					return err
				}
				if err := h.awaitSDKRunMCPUpdate(scope); err != nil {
					return err
				}
				runStarted = true
			}
			// Source drainCommandQueue creates the turn AbortController before
			// entering ask/processUserInput. Priority-now input can cancel during
			// those actual async reactions, before the first provider request.
			turnContext, interruptModel = context.WithCancelCause(modelContext)
			cancelModel = func() { interruptModel(context.Canceled) }
			task.cancelModel, task.interruptModel = cancelModel, interruptModel
			h.nativeSDKPermissionAbort = cancelModel
			endInputContext := h.beginInputContext(turnContext)
			queued, queueError := h.options.Controller.HasMainThreadQueuedCommandOnOwner(scope)
			if queueError != nil {
				endInputContext()
				cancelModel()
				return queueError
			}
			if queued {
				h.cancelSDKPromptSuggestion()
			}
			initial, err = h.serial.BeginDequeued(scope)
			endInputContext()
			if err != nil {
				cancelModel()
				return err
			}
		}
		if initial == nil {
			if cancelModel != nil {
				cancelModel()
				task.cancelModel, task.interruptModel = nil, nil
				h.nativeSDKPermissionAbort = nil
			}
			task.drainRequested = false
			if err = h.FlushHeldBackgroundResult(scope); err != nil {
				return err
			}
			if finished, err := run.finish(scope); err != nil {
				return err
			} else if finished {
				if !run.active {
					runStarted = false
				}
				continue
			}
			runStarted = false
			if h.options.Print != nil && h.options.Print.NativeLeaderInbox != nil && !h.isAgent() && h.options.Print.TeamWorker == nil {
				pending, e := h.options.Print.NativeLeaderInbox(modelContext, scope, task.closed)
				if e != nil {
					return e
				}
				if pending {
					task.drainRequested = true
					continue
				}
			}
			if task.closed {
				return h.finishSDKPromptSuggestionInput(scope)
			}
			task.wake = make(chan struct{})
			if err = scope.AwaitPromise(task.wake); err != nil {
				return err
			}
			continue
		}
		run.setPhase("draining_commands")
		err = h.consumeASK(scope, turnContext, initial, options)
		if err == nil {
			err = h.options.Controller.CompletedCommandLifecycle(scope)
		}
		if err == nil {
			h.launchSDKPromptSuggestion(scope)
		}
		retainHookSignal := err == nil && h.nativePermissionHooks.RetainTurnCancel(scope, cancelModel)
		if !retainHookSignal {
			cancelModel()
			task.cancelModel = nil
			task.interruptModel = nil
			h.nativeSDKPermissionAbort = nil
		}
		if err != nil {
			return err
		}
	}
}

// print.run awaits updateSdkMcp once before its dequeue loop. Even the real
// empty-config body returns an async promise; the input task keeps consuming
// control replies independently while this same owner awaits that return.
func (h *Host) awaitSDKRunMCPUpdate(scope *cooperative.Scope) error {
	if h.options.NativeSDKPrepareTurn == nil {
		return nil
	}
	returned, err := NewOwnerFuture[struct{}](h.options.Owner)
	if err != nil {
		return err
	}
	if failure := h.options.NativeSDKPrepareTurn(scope); failure != nil {
		err = returned.Reject(scope, failure)
	} else {
		err = returned.Resolve(scope, struct{}{})
	}
	if err != nil {
		return err
	}
	_, err = returned.Await(scope)
	return err
}

func (h *Host) consumeASK(scope *cooperative.Scope, modelContext context.Context, initial *protocol.JSONObject, options StreamOptions) (resultErr error) {
	finalized := false
	finalize := func(scope *cooperative.Scope) error {
		if finalized {
			return nil
		}
		finalized = true
		return h.serial.Finally(scope)
	}
	defer func() {
		h.emitIterator = nil
		if err := finalize(scope); resultErr == nil {
			resultErr = err
		}
	}()
	started, _ := NewOwnerFuture[struct{}](h.options.Owner)
	producerStarted := false
	submit, err := NewOwnerIterator(h.options.Owner, func(scope *cooperative.Scope, iterator *OwnerIterator[[]byte], request IteratorRequest[[]byte]) error {
		if request.Returning {
			return cap("ASK consumer return")
		}
		if err := iterator.HoldProducer(scope); err != nil {
			return err
		}
		producerStarted = true
		return started.Resolve(scope, struct{}{})
	})
	if err != nil {
		return err
	}
	ask, err := NewOwnerIterator(h.options.Owner, func(scope *cooperative.Scope, iterator *OwnerIterator[[]byte], request IteratorRequest[[]byte]) error {
		return iterator.Delegate(scope, submit, request, func(scope *cooperative.Scope, _ IteratorResult[[]byte], request IteratorRequest[[]byte]) error {
			if err := finalize(scope); err != nil {
				return err
			}
			if request.Returning {
				return iterator.CompleteReturn(scope)
			}
			return iterator.Complete(scope)
		}, func(scope *cooperative.Scope, failure error) error {
			if err := finalize(scope); err != nil {
				return err
			}
			return iterator.Fail(scope, failure)
		})
	})
	if err != nil {
		return err
	}
	consumer := ask
	if options.WrapASK != nil {
		consumer, err = options.WrapASK(ask)
		if err != nil || consumer == nil || consumer.AsyncIteratorOwner() != h.options.Owner {
			return cap("ASK observational wrapper owner")
		}
	}
	completed, _ := NewOwnerFuture[struct{}](h.options.Owner)
	rejectConsumer := func(scope *cooperative.Scope, failure error) error {
		if !producerStarted {
			_ = started.Reject(scope, failure)
		}
		return completed.Reject(scope, failure)
	}
	var outputFailure error
	var consume func(*cooperative.Scope) error
	consume = func(scope *cooperative.Scope) error {
		next, err := consumer.Next(scope)
		if err != nil {
			return err
		}
		_, err = next.Then(scope, func(scope *cooperative.Scope, result IteratorResult[[]byte], err error) error {
			if err != nil {
				return rejectConsumer(scope, err)
			}
			if result.Done {
				if !producerStarted {
					failure := cap("ASK wrapper completed before original producer")
					return rejectConsumer(scope, failure)
				}
				h.options.OnPhase("lifecycle:completed")
				return completed.Resolve(scope, struct{}{})
			}
			if _, err = h.deliverOutput(scope, result.Value); err != nil {
				outputFailure = err
				returned, err := consumer.Return(scope, nil)
				if err != nil {
					return rejectConsumer(scope, outputFailure)
				}
				_, err = returned.Then(scope, func(scope *cooperative.Scope, _ IteratorResult[[]byte], _ error) error {
					return rejectConsumer(scope, outputFailure)
				})
				if err != nil {
					return rejectConsumer(scope, outputFailure)
				}
				return err
			}
			if err := consume(scope); err != nil {
				return rejectConsumer(scope, err)
			}
			return nil
		})
		return err
	}
	if err = consume(scope); err != nil {
		return err
	}
	if err = scope.AwaitPromise(started.Done()); err != nil {
		return err
	}
	if _, err = started.Wait(context.Background()); err != nil {
		return err
	}
	h.emitIterator = submit
	failure := h.runAccepted(scope, modelContext, initial)
	if failure != nil {
		err = submit.Fail(scope, failure)
	} else {
		err = submit.Complete(scope)
	}
	if err == nil {
		_, err = completed.Await(scope)
	}
	h.emitIterator = nil
	return err
}

func (h *Host) yieldIterator(scope *cooperative.Scope, data []byte) (requestretry.NoticeAction, error) {
	resumed, _ := NewOwnerFuture[IteratorRequest[[]byte]](h.options.Owner)
	err := h.emitIterator.Yield(scope, append([]byte(nil), data...), func(scope *cooperative.Scope, iterator *OwnerIterator[[]byte], request IteratorRequest[[]byte]) error {
		if err := iterator.HoldProducer(scope); err != nil {
			return err
		}
		return resumed.Resolve(scope, request)
	})
	if err != nil {
		return 0, err
	}
	if err = scope.AwaitPromise(resumed.Done()); err != nil {
		return 0, err
	}
	request, err := resumed.Wait(context.Background())
	if err != nil {
		return 0, err
	}
	if request.Returning {
		return requestretry.Stop, nil
	}
	return requestretry.Resume, nil
}
