// Package mobilehost is the gomobile-facing API. Kotlin/Swift call Start and
// Stop only. Room, PIN, and transfers stay in internal/app.
package mobilehost

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"lanshare/internal/app"
	"lanshare/internal/infra/config"
)

var (
	mu   sync.Mutex
	inst *app.Instance
	stop context.CancelFunc
	done chan struct{}
	port int
)

// Start runs the Share Group server in this process. dataDir is the app's
// private files directory. An empty or out-of-range port uses 8080.
// Calling Start while already running is not an error.
func Start(dataDir string, listenPort int) error {
	if dataDir == "" {
		return fmt.Errorf("data directory required")
	}
	if listenPort < 1 || listenPort > 65535 {
		listenPort = 8080
	}
	tmp := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	_ = os.Setenv("TMPDIR", tmp)
	mu.Lock()
	defer mu.Unlock()
	if inst != nil {
		return nil
	}
	cfg := config.Defaults()
	cfg.DataDir = dataDir
	cfg.Port = listenPort
	cfg.SystemPreview = false
	ctx, cancel := context.WithCancel(context.Background())
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	server, _, err := app.Start(ctx, cfg, log)
	if err != nil {
		cancel()
		return err
	}
	finished := make(chan struct{})
	inst = server
	stop = cancel
	done = finished
	port = listenPort
	go func() {
		defer close(finished)
		_ = server.Wait(ctx)
	}()
	return nil
}

// Stop shuts the server down. It is safe when nothing is running.
func Stop() error {
	mu.Lock()
	cancel := stop
	finished := done
	inst = nil
	stop = nil
	done = nil
	mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if finished != nil {
		<-finished
	}
	return nil
}

// URL is the Owner dashboard on this device, or empty when the server is down.
func URL() string {
	mu.Lock()
	defer mu.Unlock()
	if inst == nil {
		return ""
	}
	return inst.LocalURL()
}

// Port is the listening port, or 0 when the server is down.
func Port() int {
	mu.Lock()
	defer mu.Unlock()
	if inst == nil {
		return 0
	}
	return port
}
