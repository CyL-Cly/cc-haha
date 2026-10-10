package diagnostics

// ReadNowMillis borrows this process's elapsed clock; it does not allocate an
// Owner, timer, worker or log entry. Error-capable hosts provide the same clock
// at construction. The native int64 clock remains the default.
func (process *Process) ReadNowMillis() (float64, error) {
	if process.options.ReadNowMillis != nil {
		return process.options.ReadNowMillis()
	}
	return float64(process.options.NowMillis()), nil
}
