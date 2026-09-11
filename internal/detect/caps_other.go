//go:build !linux

package detect

func hasCaps() bool { return false }
