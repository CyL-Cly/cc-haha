// Package nativepaths combines shared native paths with permission rule matching.
package nativepaths

import "github.com/NanmiCoder/cc-haha/backend/internal/windowspaths"

func IsUNC(value string) bool                 { return windowspaths.IsUNC(value) }
func WindowsToPOSIX(value string) string      { return windowspaths.WindowsToPOSIX(value) }
func POSIXToWindows(value string) string      { return windowspaths.POSIXToWindows(value) }
func CleanWindows(value string) string        { return windowspaths.CleanWindows(value) }
func JoinWindows(values ...string) string     { return windowspaths.JoinWindows(values...) }
func DirWindows(value string) string          { return windowspaths.DirWindows(value) }
func IsWindowsAbsolute(value string) bool     { return windowspaths.IsWindowsAbsolute(value) }
func ResolveWindows(value, cwd string) string { return windowspaths.ResolveWindows(value, cwd) }
func ExpandWindows(value, cwd, home string) (string, error) {
	return windowspaths.ExpandWindows(value, cwd, home)
}
func RelativeWindows(from, to string) string { return windowspaths.RelativeWindows(from, to) }
func driveLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
