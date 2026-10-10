package whatsappcandidate

import (
	"sync"
)

type nodeFileQueue struct {
	mu   sync.Mutex
	tail chan struct{}
}

var nodeFileQueues sync.Map

// Reservation occurs synchronously, before starting the physical operation.
// A subsequent get takes the same queue position as the original async mutex.
func reserveNodeFile(path string) (func(), func()) {
	raw, _ := nodeFileQueues.LoadOrStore(path, &nodeFileQueue{})
	q := raw.(*nodeFileQueue)
	q.mu.Lock()
	if q.tail == nil {
		q.tail = make(chan struct{})
		close(q.tail)
	}
	prior := q.tail
	done := make(chan struct{})
	q.tail = done
	q.mu.Unlock()
	var once sync.Once
	return func() { <-prior }, func() { once.Do(func() { close(done) }) }
}
