package provider

import "sync"

// NativeHTTP1HeadInitialization records only admission of the actual native
// dial/write resource. It is not TCP, request-write or response readiness.
// Only this package can settle it, after reserve started that physical job.
type NativeHTTP1HeadInitialization struct {
	done chan struct{}
	once sync.Once
}

func NewNativeHTTP1HeadInitialization() *NativeHTTP1HeadInitialization {
	return &NativeHTTP1HeadInitialization{done: make(chan struct{})}
}

func (initialization *NativeHTTP1HeadInitialization) Done() <-chan struct{} {
	if initialization == nil {
		return nil
	}
	return initialization.done
}

func (initialization *NativeHTTP1HeadInitialization) admitted() {
	if initialization != nil && initialization.done != nil {
		initialization.once.Do(func() { close(initialization.done) })
	}
}
