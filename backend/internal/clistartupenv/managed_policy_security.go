package clistartupenv

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Managed policy security uses the same Source safe-env authority as trusted
// settings application; this is not a second allowlist. Full Unicode casing
// retains JS expansions such as NONEßENTIAL -> NONESSENTIAL.
func IsSafeManagedEnvironmentKey(key string) bool {
	return safeKeys[cases.Upper(language.Und).String(key)]
}
