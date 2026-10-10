// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package appstate

import "errors"

// Errors that this package can return.
var (
	ErrMissingPreviousSetValueOperation = initializeAppStateError("missing value MAC of previous SET operation")
	ErrMismatchingLTHash                = initializeAppStateError("mismatching LTHash")
	ErrMismatchingPatchMAC              = initializeAppStateError("mismatching patch MAC")
	ErrMismatchingContentMAC            = initializeAppStateError("mismatching content MAC")
	ErrMismatchingIndexMAC              = initializeAppStateError("mismatching index MAC")
	ErrKeyNotFound                      = initializeAppStateError("didn't find app state key")
)

func initializeAppStateError(message string) error {
	return errors.New(message)
}
