package whatsappcandidate

import (
	"context"
	"time"
)

func (n *NodeDeviceStore) commitSessionMutations(ctx context.Context, values map[string]any) error {
	var last error
	for attempt := 0; attempt < 10; attempt++ {
		last = n.cache.Set(ctx, map[string]map[string]any{"session": values})
		if last == nil {
			return nil
		}
		if attempt == 9 {
			return last
		}
		if n.CommitSleep != nil {
			if e := n.CommitSleep(ctx, 3*time.Second); e != nil {
				return e
			}
		} else {
			timer := time.NewTimer(3 * time.Second)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
	return last
}
