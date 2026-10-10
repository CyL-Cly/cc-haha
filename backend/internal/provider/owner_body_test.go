package provider

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// An invalid external boundary is deliberately used only for admission guards;
// none of these cases is a native transport or business parity claim.
type bodyGuardBoundary struct{ failure error }

func (b *bodyGuardBoundary) CheckOwner() error { return b.failure }
func (*bodyGuardBoundary) ArmFetchTimer(time.Duration, func()) (NativeOwnerTimer, error) {
	panic("unadmitted timer")
}
func (*bodyGuardBoundary) AwaitHeaders(NativeOwnerOperation) ([]byte, error) {
	panic("unadmitted open")
}
func (*bodyGuardBoundary) RejectFetch(error) error     { panic("unadmitted reject") }
func (*bodyGuardBoundary) IsAdministrative(error) bool { return true }

func TestNativeBodyDriverRejectsAbsentForeignAndStaleLeaseBeforeIO(t *testing.T) {
	awaitCalls := 0
	await := func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
		awaitCalls++
		return nil, nil
	}
	native := &OwnerNativeIO{boundary: &bodyGuardBoundary{}}
	for _, value := range []struct {
		native *OwnerNativeIO
		await  OwnerAwait
	}{{nil, await}, {native, nil}} {
		if _, err := NewOwnerBodyIO(value.native, value.await); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal(err)
		}
	}
	stopped := errors.New("original ASK scope expired")
	bad := &bodyGuardBoundary{failure: stopped}
	if _, err := NewOwnerBodyIO(&OwnerNativeIO{boundary: bad}, await); !IsOwnerWaitFailure(err) || !errors.Is(err, stopped) {
		t.Fatal(err)
	}
	driver, err := NewOwnerBodyIO(native, await)
	if err != nil {
		t.Fatal(err)
	}
	var absent *OwnerBodyIO
	if _, err = absent.streamBody(io.NopCloser(strings.NewReader("foreign"))); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	if _, err = (&OwnerBodyIO{}).streamBody(io.NopCloser(strings.NewReader("foreign"))); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	for _, body := range []io.ReadCloser{io.NopCloser(strings.NewReader("foreign")), &nativeOwnerBody{driver: native}, &nativeOwnerBody{driver: &OwnerNativeIO{boundary: &bodyGuardBoundary{}}, physical: &nativeBodyReadLease{}}} {
		if _, err = driver.streamBody(body); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal("foreign native stream lease admitted", err)
		}
		if _, err = driver.errorBody(body); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal("foreign native error lease admitted", err)
		}
	}
	native.boundary = bad
	if _, err = driver.streamBody(io.NopCloser(strings.NewReader("foreign"))); !IsOwnerWaitFailure(err) || !errors.Is(err, stopped) {
		t.Fatal("previous ASK admitted", err)
	}
	if awaitCalls != 0 {
		t.Fatal("unadmitted body worker ran", awaitCalls)
	}
}

func TestNativeBodyReaderScopeFailureClosesRealResourceAndNeverReads(t *testing.T) {
	listener := tlsProbeListener(t)
	address := listener.Addr().String()
	listener.Close()
	resource, err := NewNativeHTTP1Reservation(context.Background(), address, []byte("original"), 64)
	if err != nil {
		t.Fatal(err)
	}
	if err = resource.WaitReadable(context.Background()); err == nil {
		t.Fatal("private closed listener accepted")
	}
	original := errors.New("original ASK returned")
	driver := &OwnerBodyIO{native: &OwnerNativeIO{boundary: &bodyGuardBoundary{failure: original}}, await: func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
		t.Error("stale scope started physical read")
		return nil, nil
	}}
	lease := &nativeOwnerBody{driver: driver.native, resource: resource, physical: &nativeBodyReadLease{resource: resource}}
	body := &nativeBodyReader{driver: driver, lease: lease}
	out := []byte("unchanged")
	count, err := body.Read(out)
	if count != 0 || string(out) != "unchanged" || !IsOwnerWaitFailure(err) || !errors.Is(err, original) {
		t.Fatal("scope failure changed bytes/owner error", count, err)
	}
	if err = body.Close(); err != nil {
		t.Fatal(err)
	}
	if lease.physical.beginRead() {
		t.Fatal("closed descriptor admitted late reader")
	}
	state, err := InspectNativeOwnerBody(lease)
	if err != nil || !state.Closed || !state.Joined {
		t.Fatal(state, err)
	}
	// A header-only existing observer lease remains safe to Close as well.
	if err = (&nativeOwnerBody{resource: resource}).Close(); err != nil {
		t.Fatal(err)
	}
}
