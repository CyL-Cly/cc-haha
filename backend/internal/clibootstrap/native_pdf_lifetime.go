package clibootstrap

import (
	"context"
	"sync"
)

// Source FileReadTool drains an admitted PDF call before it observes a user
// interrupt. Physical PDF jobs therefore belong to the runtime, rather than
// the individual ASK abort controller. Actual runtime closure still cancels
// and joins these jobs through its existing NativeBinding.
type nativePDFLifetime struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
}

func (life *nativePDFLifetime) context() context.Context {
	life.mu.Lock()
	defer life.mu.Unlock()
	if life.ctx == nil {
		life.ctx, life.cancel = context.WithCancel(context.Background())
		if life.closed {
			life.cancel()
		}
	}
	return life.ctx
}
func (life *nativePDFLifetime) close() {
	life.mu.Lock()
	defer life.mu.Unlock()
	life.closed = true
	if life.cancel != nil {
		life.cancel()
	}
}

// BeginNativePDFRuntimeClose is the actual host close boundary before it joins
// a pending ASK. It is never called by ordinary user interrupt/Stop controls.
func (m *MainPrint) BeginNativePDFRuntimeClose() {
	if m != nil {
		m.nativePDFLife.close()
		m.nativeReadCountLife.close()
	}
}
