package builtinwrite

// DiffLineCounts exposes the same untimed diff.js line path used by checkpoint
// patches. Insights counts changed lines without mutating the write ledger.
func DiffLineCounts(before, after string) (added, removed int) {
	for _, hunk := range StructuredPatchUnbounded(before, after) {
		for _, line := range hunk.Lines {
			if len(line) == 0 {
				continue
			}
			if line[0] == '+' {
				added++
			}
			if line[0] == '-' {
				removed++
			}
		}
	}
	return
}
