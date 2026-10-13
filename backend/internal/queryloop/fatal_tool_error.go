package queryloop

import "errors"

// A Source configuration failure that terminates the CLI is distinct from
// recoverable Tool.call/permission response failures sent back to the model.
func fatalToolError(err error) bool {
	var failure interface{ FatalToolConfiguration() bool }
	return errors.As(err, &failure) && failure.FatalToolConfiguration()
}
