package clisettings

// NativeSourceWriterBindings supplies the original asynchronous local-write
// Git-ignore producer. Finite/ordinary writers retain their existing guard.
type NativeSourceWriterBindings struct{ StartLocalIgnore func(string, string) error }

func (s *Service) UpdateNativeEditableSource(source, projectOverride string, patch Patch, bindings NativeSourceWriterBindings) (WriteResult, error) {
	if s.options.Profile != NativeProcessProfile || bindings.StartLocalIgnore == nil {
		return WriteResult{}, &CapabilityError{"native local settings Git-ignore producer"}
	}
	return s.updateEditableSource(source, projectOverride, patch, &bindings)
}
