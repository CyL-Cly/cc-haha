package cooperative

// RejectFetch resumes the finite native-creation rejection continuation on the
// original owner. Unlike a ready native response header, this rejection must
// admit already-due timer policy before the SDK fetch finally. It does not wait
// for a guessed Promise delay or claim arbitrary Bun task ordering.
func (scope *Scope) RejectFetch(failure error) error {
	if err := scope.check(); err != nil {
		return err
	}
	if failure == nil || scope.callback != 0 || scope.frame != 0 {
		return ErrRestricted
	}
	scope.fireDueTimers()
	return failure
}
