package app

import (
	"context"
	"net"
)

// tcpBufSize is the socket receive/send window requested for every accepted
// connection. A few MiB helps large uploads and downloads fill a gigabit or
// USB link without waiting for the window to grow.
const tcpBufSize = 4 << 20

func listenTCP(ctx context.Context, addr string) (net.Listener, error) {
	lc := net.ListenConfig{Control: tuneListenSocket}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return tcpListener{Listener: ln}, nil
}

type tcpListener struct{ net.Listener }

func (l tcpListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(tcpBufSize)
		_ = tc.SetWriteBuffer(tcpBufSize)
	}
	return c, nil
}
