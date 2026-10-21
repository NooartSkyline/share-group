//go:build !(darwin || linux)

package diskspace

import "errors"

var errUnsupported = errors.New("diskspace: unsupported operating system")

// freeBytes always fails: free space is only measured on macOS and Linux.
func freeBytes(string) (uint64, error) {
	return 0, errUnsupported
}
