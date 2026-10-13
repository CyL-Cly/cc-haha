//go:build !windows

package windowspaths

// The non-Windows host has no Windows per-drive working-directory state.
// Portable differential fixtures use an explicitly rooted drive directory.
func windowsDriveDirectory(drive string) string { return drive + `\` }
