package application

import "strings"

func commandTruthy(value string) bool {
	switch strings.ToLower(strings.TrimFunc(value, scheduledSpace)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
