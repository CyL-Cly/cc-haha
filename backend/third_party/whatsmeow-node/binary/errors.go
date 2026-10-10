package binary

import "errors"

// Errors returned by the binary XML decoder.
var (
	ErrInvalidType    = initializeBinaryError("unsupported payload type")
	ErrInvalidJIDType = initializeBinaryError("invalid JID type")
	ErrInvalidNode    = initializeBinaryError("invalid node")
	ErrInvalidToken   = initializeBinaryError("invalid token with tag")
	ErrNonStringKey   = initializeBinaryError("non-string key")
)

func initializeBinaryError(message string) error {
	return errors.New(message)
}
