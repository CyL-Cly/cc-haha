package whatsappcandidate

import (
	"context"
	"sort"
	"sync"
)

type nodeDeviceTransactionKey struct{}
type nodeDeviceTransaction struct {
	store     *NodeDeviceStore
	addresses map[string]bool
}

func (n *NodeDeviceStore) AcquireSessions(ctx context.Context, addresses []string) (context.Context, func(), error) {
	if e := ctx.Err(); e != nil {
		return ctx, nil, e
	}
	parent, _ := ctx.Value(nodeDeviceTransactionKey{}).(nodeDeviceTransaction)
	owned := map[string]bool{}
	if parent.store == n {
		for key := range parent.addresses {
			owned[key] = true
		}
	}
	ordered := append([]string{}, addresses...)
	sort.Strings(ordered)
	releases := []func(){}
	cleanup := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, address := range ordered {
		if owned[address] {
			continue
		}
		parsed, e := deviceSignalAddress(address)
		if e != nil {
			cleanup()
			return ctx, nil, e
		}
		release, e := n.Signal.acquireAddress(ctx, n.Signal.nodeAddress(parsed))
		if e != nil {
			cleanup()
			return ctx, nil, e
		}
		releases = append(releases, release)
		owned[address] = true
	}
	var once sync.Once
	return context.WithValue(ctx, nodeDeviceTransactionKey{}, nodeDeviceTransaction{n, owned}), func() { once.Do(cleanup) }, nil
}
