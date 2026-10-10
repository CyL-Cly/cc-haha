package cooperative

// Test-only accessors preserve the existing native reservation test's exact
// due-channel barrier and owner-only active-ticket assertion. External tests
// may import the high-level provider without adding a package test cycle.
func TimerDueForTesting(ticket *TimerTicket) <-chan struct{} { return ticket.due }
func TimerActiveForTesting(ticket *TimerTicket) bool         { return ticket.active }
