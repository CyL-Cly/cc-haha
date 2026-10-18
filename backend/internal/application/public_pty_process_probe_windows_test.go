//go:build windows

package application

import "errors"

// Unix PTY entry tests keep their Windows skips. This stub only makes their
// test package compilable; it is not a Windows process or ConPTY assertion.
func publicPTYProcessSignalZero(int) error {
	return errors.ErrUnsupported
}
