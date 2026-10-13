package server

import "context"

// A registered user exists before the shared runtime is ready. Admission and
// CLI/background execution have separate lifetimes and cannot describe this
// client-facing foreground domain. All helpers require runtime.mu ownership.
type registeredUserTurn struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func registerUserTurn(runtime *runtimeSession) *registeredUserTurn {
	ctx, cancel := context.WithCancel(runtime.ctx)
	turn := &registeredUserTurn{ctx: ctx, cancel: cancel}
	runtime.registeredUserTurn = turn
	return turn
}

func hasRegisteredUserTurn(runtime *runtimeSession) bool {
	return runtime.registeredUserTurn != nil && runtime.registeredUserTurn.ctx.Err() == nil
}

func settleRegisteredUserTurn(runtime *runtimeSession, turn *registeredUserTurn) {
	if turn == nil {
		return
	}
	turn.cancel()
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
}
