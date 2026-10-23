// Package app is the composition root shared by cmd/lanshare and the mobile
// host shells. It wires adapters into use cases and serves HTTP. Domain rules
// stay in usecase; this package is allowed to import every layer.
package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"lanshare/internal/adapter/caffeinate"
	"lanshare/internal/adapter/cmdrunner"
	"lanshare/internal/adapter/diskspace"
	"lanshare/internal/adapter/filestore"
	"lanshare/internal/adapter/heicconv"
	"lanshare/internal/adapter/hostfs"
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

// Ready is what a CLI banner needs after the server is listening.
type Ready struct {
	Cfg     config.Config
	Room    domain.Room
	Network *network.Service
}

// Instance is a running Share Group server.
type Instance struct {
	port     int
	srv      *http.Server
	transfer *transfer.Service
	power    *power.Guard
	log      *slog.Logger
	errc     chan error
}

// LocalURL is the Owner dashboard on this device.
func (i *Instance) LocalURL() string {
	if i == nil {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", i.port)
}

// Start wires adapters, listens, and serves in the background. ctx cancels
// background jobs; call Shutdown to stop the HTTP server.
func Start(ctx context.Context, cfg config.Config, log *slog.Logger) (*Instance, *Ready, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create data dir: %w", err)
	}

	clock := system.Clock{}
	ids := system.IDGenerator{}
	store, err := jsonstore.Open(filepath.Join(cfg.DataDir, "state.json"))
	if err != nil {
		return nil, nil, err
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

	roomSvc := room.NewService(store.RoomRepo(), clock, rand.Reader)
	authSvc := auth.NewService(store.RoomRepo(), devices, tokens, limiter, ids, clock, hub)
	deviceSvc := device.NewService(devices, hub)
	hub.SetPresence(deviceSvc)
	transferSvc := transfer.NewService(store.FileRepo(), files, files, thumb.New(), hub, ids, clock, 250*time.Millisecond)
	chatSvc := chat.NewService(memory.NewChatRepository(), hub, ids, clock)
	runner := cmdrunner.New()
	networkSvc := network.NewService(netinfo.New(cfg.DataDir), cfg.Port, cfg.Interface)
	networkSvc.SetRunner(runner)
	networkSvc.SetHostBrowser(hostfs.New(cfg.DataDir))
	transferSvc.SetRooms(store.RoomRepo())
	if cfg.TLS {
		networkSvc.SetSecure(true)
	}

	if cfg.SystemPreview && syspreview.Available() {
		transferSvc.SetSystemPreviewer(syspreview.New(cmdrunner.New()))
	}
	if cfg.SystemPreview && heicconv.Available() {
		transferSvc.SetHEICConverter(heicconv.New(cmdrunner.New()))
	}

	powerGuard := power.NewGuard(clock, caffeinate.NewLauncher(), os.Getpid(), power.DefaultGrace, cfg.KeepAwake)

	transferSvc.ConfigureUploads(transfer.UploadDeps{
		Sessions: sessions,
		Partials: files.Partials(),
		Guard:    storage.NewGuard(disk, uint64(cfg.MinFree), log),
		Secrets:  system.SecretGenerator{},
		Activity: powerGuard,
	}, transfer.UploadConfig{
		TTL:          cfg.UploadTTL,
		StallTimeout: cfg.StallTimeout,
	})

	exePath, err := os.Executable()
	if err != nil {
		log.Warn("cannot find own executable path; firewall check will be limited", "error", err)
		exePath = ""
	}
	peers := peerlog.New(clock)
	nics := netinfo.New(cfg.DataDir)
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

	log.Info("config",
		"min-free", config.FormatSize(cfg.MinFree),
		"upload-ttl", cfg.UploadTTL.String(),
		"stall-timeout", cfg.StallTimeout.String(),
		"keep-awake", cfg.KeepAwake,
		"system-preview", cfg.SystemPreview,
		"tls", cfg.TLS)

	if err := transferSvc.Recover(ctx); err != nil {
		return nil, nil, fmt.Errorf("recover upload sessions: %w", err)
	}
	go transferSvc.RunJanitor(ctx)
	go powerGuard.Run(ctx)

	defaultRoom, err := roomSvc.EnsureDefault(ctx, domain.RoomID(cfg.RoomID), cfg.RoomName)
	if err != nil {
		return nil, nil, err
	}

	webFS, err := fs.Sub(web.Files, ".")
	if err != nil {
		return nil, nil, err
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
	}

	ln, err := listenTCP(ctx, srv.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen on %s: %w", srv.Addr, err)
	}

	certFile, keyFile := "", ""
	if cfg.TLS {
		var ips []net.IP
		if list, ierr := netinfo.New(cfg.DataDir).Interfaces(ctx); ierr == nil {
			for _, n := range list {
				if ip := net.ParseIP(n.IP); ip != nil {
					ips = append(ips, ip)
				}
			}
		}
		var err error
		certFile, keyFile, err = tlscert.Ensure(filepath.Join(cfg.DataDir, "tls"), ips)
		if err != nil {
			_ = ln.Close()
			return nil, nil, fmt.Errorf("tls: %w", err)
		}
	}

	go networkSvc.KeepCable(ctx, defaultRoom.ID)

	errc := make(chan error, 1)
	go func() {
		var err error
		if cfg.TLS {
			err = srv.ServeTLS(ln, certFile, keyFile)
		} else {
			err = srv.Serve(ln)
		}
		errc <- err
	}()

	inst := &Instance{
		port:     cfg.Port,
		srv:      srv,
		transfer: transferSvc,
		power:    powerGuard,
		log:      log,
		errc:     errc,
	}
	ready := &Ready{Cfg: cfg, Room: defaultRoom, Network: networkSvc}
	return inst, ready, nil
}

// Wait blocks until ctx ends or the HTTP server fails.
func (i *Instance) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return i.Shutdown()
	case err := <-i.errc:
		i.stopPower()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Shutdown drains transfers and stops HTTP and the power guard.
func (i *Instance) Shutdown() error {
	if i == nil {
		return nil
	}
	i.log.Info("shutting down")
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 3*time.Second)
	i.transfer.BeginShutdown(drainCtx)
	cancelDrain()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.srv.Shutdown(shutCtx); err != nil {
		_ = i.srv.Close()
	}
	i.stopPower()
	return nil
}

func (i *Instance) stopPower() {
	if i.power == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := i.power.Stop(ctx); err != nil {
		i.log.Warn("stop power guard", "error", err)
	}
}
