package provider

import "time"

// All clock facts and callbacks belong to the original owner. The fetch
// wrapper's token refresh, encoding and buffered response use this same ticket
// as the physical upstream call; only the remapped response clears it.
type nativeFetchClock struct {
	driver   *OwnerNativeIO
	ticket   NativeOwnerTimer
	resource *NativeHTTP1Reservation
	timedOut bool
}

func (driver *OwnerNativeIO) armFetchClock(delay time.Duration, cancel func()) (*nativeFetchClock, error) {
	clock := &nativeFetchClock{driver: driver}
	ticket, err := driver.boundary.ArmFetchTimer(delay, func() {
		clock.timedOut = true
		driver.event("timer-fire", 0, nil)
		if cancel != nil {
			cancel()
		}
		if clock.resource != nil {
			_ = clock.resource.Close()
		}
	})
	if err != nil {
		return nil, &ownerWaitFailure{err}
	}
	clock.ticket = ticket
	driver.event("timer-arm", 0, nil)
	return clock, nil
}
func (clock *nativeFetchClock) clear() {
	if clock.ticket != nil {
		_ = clock.ticket.Clear()
		clock.ticket = nil
		clock.driver.event("timer-clear", 0, nil)
	}
}
