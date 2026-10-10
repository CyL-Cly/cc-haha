package toolschema

// FormatFileSize exposes the already Source-tested src/utils/format primitive
// shared by Read's prompt and its terminal result renderer.
func FormatFileSize(size float64) string { return formatFileSize(size) }
