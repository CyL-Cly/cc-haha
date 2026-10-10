//go:build !darwin && !linux

package sessioncontroller

func noFollowFlag() int { return 0 }
