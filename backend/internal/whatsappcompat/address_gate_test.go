package whatsappcandidate

import (
	"context"
	"testing"
	"time"
)

func TestNodeSignalAddressTransactionsSerializeSameAndPermitIndependentAddresses(t *testing.T) {
	store := NodeSignalStore{}
	ctx := context.Background()
	first, e := store.acquireAddress(ctx, "111.0")
	if e != nil {
		t.Fatal(e)
	}
	same := make(chan func(), 1)
	go func() {
		release, e := store.acquireAddress(ctx, "111.0")
		if e != nil {
			return
		}
		same <- release
	}()
	other, e := store.acquireAddress(ctx, "222.0")
	if e != nil {
		first()
		t.Fatal(e)
	}
	other()
	select {
	case release := <-same:
		release()
		first()
		t.Fatal("same address escaped active crypto transaction")
	default:
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := store.acquireAddress(canceled, "111.0"); e != context.Canceled {
		first()
		t.Fatal(e)
	}
	first()
	select {
	case release := <-same:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("same address transaction did not settle")
	}
}
