package cooperative

// SubmitModuleInitialization admits actual first-time compiled-module linking
// and state evaluation. It is host initialization, not a native completion or
// an empty event-loop delay. The callback owns no worker and cannot run a Frame.
// Caller cancellation before admission rejects the request; once admitted the
// initialization belongs to Owner lifetime, independently of any one importer.
func (scope *Scope) SubmitModuleInitialization(owner *Owner, initialize func(*Scope) error) (*Receipt, error) {
	if owner == nil || !scope.BelongsTo(owner) || initialize == nil {
		return nil, ErrRestricted
	}
	if err := scope.check(); err != nil {
		return nil, err
	}
	return owner.enqueue(owner.life, Read, initialize, true)
}
