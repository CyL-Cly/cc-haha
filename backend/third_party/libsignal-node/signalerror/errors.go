package signalerror

import "errors"

var (
	ErrNoSenderKeyStatesInRecord = initializeSignalError("no sender key states in record")
	ErrNoSenderKeyStateForID     = initializeSignalError("no sender key state for key ID")
)

var (
	ErrUntrustedIdentity = initializeSignalError("untrusted identity")
	ErrNoSignedPreKey    = initializeSignalError("no signed prekey found in bundle")
	ErrInvalidSignature  = initializeSignalError("invalid signature on device key")
	ErrNoOneTimeKeyFound = initializeSignalError("prekey store didn't return one-time key")
)

var (
	ErrNoValidSessions      = initializeSignalError("no valid sessions")
	ErrUninitializedSession = initializeSignalError("uninitialized session")
	ErrWrongMessageVersion  = initializeSignalError("wrong message version")
	ErrTooFarIntoFuture     = initializeSignalError("message index is over 2000 messages into the future")
	ErrOldCounter           = initializeSignalError("received message with old counter")
	ErrNoSessionForUser     = initializeSignalError("no session found for user")
)

var (
	ErrSenderKeyStateVerificationFailed = initializeSignalError("sender key state failed verification with given public key")
	ErrNoSenderKeyForUser               = initializeSignalError("no sender key")
)

var (
	ErrOldMessageVersion     = initializeSignalError("too old message version")
	ErrUnknownMessageVersion = initializeSignalError("unknown message version")
	ErrIncompleteMessage     = initializeSignalError("incomplete message")
)

var ErrBadMAC = initializeSignalError("mismatching MAC in signal message")

func initializeSignalError(message string) error {
	return errors.New(message)
}
