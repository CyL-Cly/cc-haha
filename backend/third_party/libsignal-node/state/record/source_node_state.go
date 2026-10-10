package record

// CopyForDecryption retains this state's serializer and isolates speculative
// ratchet changes until authentication succeeds.
func (s *State) CopyForDecryption() (*State, error) {
	return NewStateFromBytes(s.Serialize(), s.serializer)
}
