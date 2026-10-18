package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"sync"
)

type nativeLSPLifetime struct {
	mu       sync.Mutex
	jobs     sync.WaitGroup
	closed   bool
	borrowed bool
	binding  *ownerasync.NativeBinding
}
