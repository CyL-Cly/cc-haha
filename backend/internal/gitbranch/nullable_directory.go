package gitbranch

import "path/filepath"

// pathJoinNullError ports the pinned Bun path.join argument-zero rejection.
// Source's refsDir is read after await; null must not become Go's empty string
// and an actual relative CWD watch. Native runtime exception dispatch remains
// a separate gate; this producer preserves the rejected Future name/code/text.
type pathJoinNullError struct{}

func (*pathJoinNullError) Error() string {
	return `The "paths[0]" property must be of type string, got object`
}
func (*pathJoinNullError) ErrorName() string { return "TypeError" }
func (*pathJoinNullError) ErrorCode() string { return "ERR_INVALID_ARG_TYPE" }

// Resolved Git/common directory producers return absolute strings or null.
func nullableDirectory(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// Nullish choice happens at the real caller's read, never snapshotted before
// an await. A present empty string remains a string for path.join.
func joinNullableDirectory(directory *string, parts ...string) (string, error) {
	if directory == nil {
		return "", &pathJoinNullError{}
	}
	return filepath.Join(append([]string{*directory}, parts...)...), nil
}
