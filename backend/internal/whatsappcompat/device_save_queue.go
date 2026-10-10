package whatsappcandidate

import (
	"context"
	"fmt"
	"os"
	"sync"
)

type nodeCredsSaveQueue struct {
	mu   sync.Mutex
	tail chan struct{}
}

var nodeCredsSaveQueues sync.Map
var nodeCredsSaveQueuesMu sync.Mutex

func (n *NodeDeviceStore) enqueueCredsSaveLocked() error {
	path := n.Signal.Dir
	nodeCredsSaveQueuesMu.Lock()
	defer nodeCredsSaveQueuesMu.Unlock()
	raw, _ := nodeCredsSaveQueues.LoadOrStore(path, &nodeCredsSaveQueue{})
	q := raw.(*nodeCredsSaveQueue)
	q.mu.Lock()
	if q.tail == nil {
		q.tail = make(chan struct{})
		close(q.tail)
	}
	prior := q.tail
	done := make(chan struct{})
	q.tail = done
	q.mu.Unlock()
	go func() {
		<-prior
		n.mu.Lock()
		n.Signal.credsMu.Lock()
		err := n.saveCredsLocked()
		n.Signal.credsMu.Unlock()
		n.mu.Unlock()
		if err != nil {
			if n.Warn != nil {
				n.Warn(err)
			} else {
				fmt.Fprintln(os.Stderr, "[WhatsApp] Failed to save credentials:", err)
			}
		}
		nodeCredsSaveQueuesMu.Lock()
		q.mu.Lock()
		if q.tail == done {
			nodeCredsSaveQueues.Delete(path)
		}
		q.mu.Unlock()
		close(done)
		nodeCredsSaveQueuesMu.Unlock()
	}()
	return nil
}
func (n *NodeDeviceStore) WaitCredsSave(ctx context.Context) error {
	raw, exists := nodeCredsSaveQueues.Load(n.Signal.Dir)
	if !exists {
		return nil
	}
	q := raw.(*nodeCredsSaveQueue)
	q.mu.Lock()
	tail := q.tail
	q.mu.Unlock()
	if tail == nil {
		return nil
	}
	select {
	case <-tail:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
