package projectpath

// SourceHashBytes exposes the same pinned Bun byte primitive already used by
// path sanitization. Callers own the JavaScript-string-to-UTF8 conversion.
func SourceHashBytes(value []byte) uint64 { return wyhash(0, value) }
