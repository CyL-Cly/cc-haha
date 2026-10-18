package server

import "context"

// A registered user exists before the shared runtime is ready. Admission and
// CLI/background execution have separate lifetimes and cannot describe this
// client-facing foreground domain. All helpers require runtime.mu ownership.
type registeredUserTurn struct {
	ctx     context.Context
	cancel  context.CancelFunc
	settled bool
}

func registerUserTurn(runtime *runtimeSession) *registeredUserTurn {
	ctx, cancel := context.WithCancel(runtime.ctx)
	turn := &registeredUserTurn{ctx: ctx, cancel: cancel}
	runtime.registeredUserTurn = turn
	return turn
}

func hasRegisteredUserTurn(runtime *runtimeSession) bool {
	return runtime.registeredUserTurn != nil && !runtime.registeredUserTurn.settled && runtime.registeredUserTurn.ctx.Err() == nil
}

func settleRegisteredUserTurn(runtime *runtimeSession, turn *registeredUserTurn) {
	if turn == nil {
		return
	}
	// A result closes the visible foreground lifetime while the native ASK's
	// checkpoint/finally may still be running on this admission context.
	turn.settled = true
	if runtime.registeredUserTurn == turn {
		runtime.registeredUserTurn = nil
	}
}

func cancelRegisteredUserTurn(runtime *runtimeSession) {
	if turn := runtime.registeredUserTurn; turn != nil {
		turn.cancel()
	}
	if turn := runtime.activeUserTurn; turn != nil {
		turn.cancel()
	}
	for _, frame := range runtime.admission {
		if frame.userTurn != nil {
			frame.userTurn.cancel()
		}
	}
}
