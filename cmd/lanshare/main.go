// Command lanshare runs the LAN Share server. It is the composition root:
// the only place that knows every concrete adapter and wires them together.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"lanshare/internal/adapter/filestore"
	"lanshare/internal/adapter/httpapi"
	"lanshare/internal/adapter/jsonstore"
	"lanshare/internal/adapter/memory"
	"lanshare/internal/adapter/netinfo"
	"lanshare/internal/adapter/qr"
	"lanshare/internal/adapter/thumb"
	"lanshare/internal/adapter/ws"
	"lanshare/internal/domain"
	"lanshare/internal/infra/config"
	"lanshare/internal/infra/system"
	"lanshare/internal/usecase/auth"
	"lanshare/internal/usecase/chat"
	"lanshare/internal/usecase/device"
	"lanshare/internal/usecase/network"
	"lanshare/internal/usecase/room"
	"lanshare/internal/usecase/transfer"
	"lanshare/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// infrastructure and adapters
	clock := system.Clock{}
	ids := system.IDGenerator{}
	store, err := jsonstore.Open(filepath.Join(cfg.DataDir, "state.json"))
	if err != nil {
		return err
	}
	files := filestore.New(cfg.DataDir)
	devices := memory.NewDeviceRepository()
	tokens := memory.NewTokenService(clock, cfg.SessionTTL)
	limiter := memory.NewAttemptLimiter(clock, memory.LimiterPolicy{
		MaxFailures: cfg.MaxFailures,
		Window:      10 * time.Minute,
		Lockout:     cfg.Lockout,
	})
	hub := ws.NewHub(nil, log)

	// use cases
	roomSvc := room.NewService(store.RoomRepo(), clock, rand.Reader)
	authSvc := auth.NewService(store.RoomRepo(), devices, tokens, limiter, ids, clock, hub)
	deviceSvc := device.NewService(devices, hub)
	hub.SetPresence(deviceSvc)
	transferSvc := transfer.NewService(store.FileRepo(), files, files, thumb.New(), hub, ids, clock, 250*time.Millisecond)
	chatSvc := chat.NewService(memory.NewChatRepository(), hub, ids, clock)
	networkSvc := network.NewService(netinfo.New(), cfg.Port, cfg.Interface)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	defaultRoom, err := roomSvc.EnsureDefault(ctx, domain.RoomID(cfg.RoomID), cfg.RoomName)
	if err != nil {
		return err
	}

	webFS, err := fs.Sub(web.Files, ".")
	if err != nil {
		return err
	}
	api := httpapi.NewServer(httpapi.Deps{
		Auth: authSvc, Rooms: roomSvc, Devices: deviceSvc, Transfer: transferSvc, Chat: chatSvc,
		Network: networkSvc, QR: qr.New(), Hub: hub, Web: webFS,
		DefaultRoom: defaultRoom.ID, Log: log,
	})

	srv := &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(cfg.Port)),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No overall Read/WriteTimeout: they would cut off large transfers.
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", srv.Addr, err)
	}
	printBanner(ctx, cfg, defaultRoom, networkSvc)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			_ = srv.Close()
		}
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func printBanner(ctx context.Context, cfg config.Config, r domain.Room, nw *network.Service) {
	fmt.Println()
	fmt.Println("LAN Share กำลังทำงาน")
	fmt.Printf("  เครื่องนี้ (เจ้าของ): http://localhost:%d\n", cfg.Port)
	url, err := nw.JoinURL(ctx, r.ID)
	if err != nil {
		fmt.Println("  ไม่พบเครือข่าย LAN ที่ใช้งานได้ อุปกรณ์อื่นจึงยังเข้าไม่ได้")
	} else {
		fmt.Printf("  อุปกรณ์อื่น:           %s\n", url)
		if art, err := qr.Terminal(url); err == nil {
			fmt.Println(art)
		}
	}
	fmt.Printf("  ห้อง: %s   PIN: %s\n", r.Name, r.PIN)
	fmt.Printf("  โฟลเดอร์ข้อมูล: %s\n\n", cfg.DataDir)
}
