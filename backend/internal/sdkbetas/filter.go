// Package sdkbetas implements the public CLI/SDK beta allowlist producer.
package sdkbetas

const Context1M = "context-1m-2025-08-07"

func Filter(values []string, subscriber bool) (allowed, warnings []string) {
	if len(values) == 0 {
		return nil, nil
	}
	if subscriber {
		return nil, []string{"Warning: Custom betas are only available for API key users. Ignoring provided betas."}
	}
	for _, value := range values {
		if value == Context1M {
			allowed = append(allowed, value)
		} else {
			warnings = append(warnings, "Warning: Beta header '"+value+"' is not allowed. Only the following betas are supported: "+Context1M)
		}
	}
	return allowed, warnings
}
