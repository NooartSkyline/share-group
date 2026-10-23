//go:build !unix

package app

import "syscall"

func tuneListenSocket(_, _ string, _ syscall.RawConn) error {
	return nil
}
