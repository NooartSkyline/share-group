//go:build !(darwin || linux || android || windows || ios)

package diskspace

import "errors"

var errUnsupported = errors.New("diskspace: unsupported operating system")

// freeBytes always fails: free space is only measured on supported hosts.
func freeBytes(string) (uint64, error) {
	return 0, errUnsupported
}
