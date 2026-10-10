//go:build windows

package agentcatalog

import "io/fs"

func fixtureIdentity(i fs.FileInfo) string { return "" }
