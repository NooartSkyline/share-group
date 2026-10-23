// Command lanshare runs the Share Group server. It is the composition root:
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

	"lanshare/internal/adapter/caffeinate"
	"lanshare/internal/adapter/cmdrunner"
	"lanshare/internal/adapter/diskspace"
	"lanshare/internal/adapter/filestore"
	"lanshare/internal/adapter/heicconv"
	"lanshare/internal/adapter/httpapi"
	"lanshare/internal/adapter/jsonstore"
	"lanshare/internal/adapter/memory"
	"lanshare/internal/adapter/netinfo"
	"lanshare/internal/adapter/peerlog"
	"lanshare/internal/adapter/qr"
	"lanshare/internal/adapter/sessionstore"
	"lanshare/internal/adapter/syspreview"
	"lanshare/internal/adapter/thumb"
	"lanshare/internal/adapter/ws"
	"lanshare/internal/domain"
	"lanshare/internal/infra/config"
	"lanshare/internal/infra/system"
	"lanshare/internal/infra/tlscert"
	"lanshare/internal/usecase/auth"
	"lanshare/internal/usecase/chat"
	"lanshare/internal/usecase/device"
	"lanshare/internal/usecase/diagnostics"
	"lanshare/internal/usecase/network"
	"lanshare/internal/usecase/power"
	"lanshare/internal/usecase/room"
	"lanshare/internal/usecase/storage"
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
	sessions := sessionstore.New(cfg.DataDir)
	disk := diskspace.New(cfg.DataDir)
	devices := store.DeviceRepo()
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
	runner := cmdrunner.New()
	networkSvc := network.NewService(netinfo.New(), cfg.Port, cfg.Interface)
	networkSvc.SetRunner(runner)
	transferSvc.SetRooms(store.RoomRepo())
	if cfg.TLS {
		networkSvc.SetSecure(true)
	}

	// Previews of HEIC, camera RAW, PDF, Office files and the like come from macOS itself.
	if cfg.SystemPreview && syspreview.Available() {
		transferSvc.SetSystemPreviewer(syspreview.New(cmdrunner.New()))
	}
	if cfg.SystemPreview && heicconv.Available() {
		transferSvc.SetHEICConverter(heicconv.New(cmdrunner.New()))
	}

	// Keeps the Mac awake while any transfer (upload, append, download, zip)
	// is running; it is the Activity of every transfer kind below.
	powerGuard := power.NewGuard(clock, caffeinate.NewLauncher(), os.Getpid(), power.DefaultGrace, cfg.KeepAwake)

	// Resumable uploads.
	transferSvc.ConfigureUploads(transfer.UploadDeps{
		Sessions: sessions,
		Partials: files.Partials(),
		Guard:    storage.NewGuard(disk, uint64(cfg.MinFree), log), // MinFree is 0..MaxInt64, see config
		Secrets:  system.SecretGenerator{},
		Activity: powerGuard,
	}, transfer.UploadConfig{
		TTL:          cfg.UploadTTL,
		StallTimeout: cfg.StallTimeout,
	})

	// Owner diagnostics. The server's own path is a fixed argument of the
	// firewall check; without it that check reports "unknown".
	exePath, err := os.Executable()
	if err != nil {
		log.Warn("cannot find own executable path; firewall check will be limited", "error", err)
		exePath = ""
	}
	peers := peerlog.New(clock)
	nics := netinfo.New() // lists interfaces and local addresses
	diagSvc := diagnostics.NewService(diagnostics.Deps{
		Clock:      clock,
		Network:    networkSvc,
		Interfaces: nics,
		Locals:     nics,
		Peers:      peers,
		Runner:     runner,
		Disk:       disk,
		MinFree:    uint64(cfg.MinFree),
		Partials:   transferSvc,
		Power:      powerGuard,
		ExePath:    exePath,
	})

	// The effective Phase C settings, so an Owner can confirm what the flags did.
	log.Info("config",
		"min-free", config.FormatSize(cfg.MinFree),
		"upload-ttl", cfg.UploadTTL.String(),
		"stall-timeout", cfg.StallTimeout.String(),
		"keep-awake", cfg.KeepAwake,
		"system-preview", cfg.SystemPreview,
		"tls", cfg.TLS)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Rebuild the upload sessions left by the previous run. A failure here
	// means the ledger on disk cannot be trusted, so the server does not start.
	if err := transferSvc.Recover(ctx); err != nil {
		return fmt.Errorf("recover upload sessions: %w", err)
	}
	go transferSvc.RunJanitor(ctx)
	go powerGuard.Run(ctx)

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
		Network: networkSvc, QR: qr.New(), Hub: hub, Power: powerGuard,
		Diagnostics: diagSvc, Peers: peers, Clock: clock, Web: webFS,
		DefaultRoom: defaultRoom.ID, Log: log,
	})

	srv := &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(cfg.Port)),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No overall Read/WriteTimeout: they would cut off large transfers.
	}

	ln, err := listenTCP(ctx, srv.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", srv.Addr, err)
	}

	certFile, keyFile := "", ""
	if cfg.TLS {
		var ips []net.IP
		if list, ierr := netinfo.New().Interfaces(ctx); ierr == nil {
			for _, n := range list {
				if ip := net.ParseIP(n.IP); ip != nil {
					ips = append(ips, ip)
				}
			}
		}
		var err error
		certFile, keyFile, err = tlscert.Ensure(filepath.Join(cfg.DataDir, "tls"), ips)
		if err != nil {
			return fmt.Errorf("tls: %w", err)
		}
	}

	printBanner(ctx, cfg, defaultRoom, networkSvc)
	go networkSvc.KeepCable(ctx, defaultRoom.ID)

	errc := make(chan error, 1)
	go func() {
		if cfg.TLS {
			errc <- srv.ServeTLS(ln, certFile, keyFile)
			return
		}
		errc <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		// Let uploads in flight record their bytes before the connections go.
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), 3*time.Second)
		transferSvc.BeginShutdown(drainCtx)
		cancelDrain()

		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			_ = srv.Close()
		}
		stopPower(powerGuard, log)
		return nil
	case err := <-errc:
		stopPower(powerGuard, log)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// stopPower ends the caffeinate process once no request can start a transfer.
func stopPower(g *power.Guard, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := g.Stop(ctx); err != nil {
		log.Warn("stop power guard", "error", err)
	}
}

func printBanner(ctx context.Context, cfg config.Config, r domain.Room, nw *network.Service) {
	fmt.Println()
	fmt.Println("Share Group กำลังทำงาน")
	scheme := "http"
	if cfg.TLS {
		scheme = "https"
	}
	fmt.Printf("  เครื่องนี้ (เจ้าของ): %s://localhost:%d\n", scheme, cfg.Port)
	url, err := nw.JoinURL(ctx, r.ID)
	if err != nil {
		fmt.Println("  ไม่พบเครือข่าย LAN ที่ใช้งานได้ อุปกรณ์อื่นจึงยังเข้าไม่ได้")
	} else {
		fmt.Printf("  อุปกรณ์อื่น:           %s\n", url)
		if art, err := qr.Terminal(url); err == nil {
			fmt.Println(art)
		}
	}
	fmt.Printf("  สาย USB: เสียบสายแล้วเปิด %s://127.0.0.1:%d/?r=%s กรอก PIN เดียวกับ Wi-Fi (ไม่เปิดเซิร์ฟเวอร์ใหม่)\n", scheme, cfg.Port, r.ID)
	if st, err := nw.Cable(ctx, domain.Device{Role: domain.RoleOwner, RoomID: r.ID}); err == nil && len(st.Links) > 0 && st.Links[0].URL != "" {
		fmt.Printf("           พร้อมแล้ว:         %s\n", st.Links[0].URL)
	}
	fmt.Printf("  ห้อง: %s   PIN: %s\n", r.Name, r.PIN)
	fmt.Printf("  โฟลเดอร์ข้อมูล: %s\n\n", cfg.DataDir)
}
