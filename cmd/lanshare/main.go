// Command lanshare runs the Share Group server. It loads flags, then hands
// composition to package app — the same root the Android/iOS shells call.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"lanshare/internal/adapter/qr"
	"lanshare/internal/app"
	"lanshare/internal/domain"
	"lanshare/internal/infra/config"
	"lanshare/internal/usecase/network"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	inst, ready, err := app.Start(ctx, cfg, log)
	if err != nil {
		return err
	}
	printBanner(ctx, ready.Cfg, ready.Room, ready.Network)
	return inst.Wait(ctx)
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
