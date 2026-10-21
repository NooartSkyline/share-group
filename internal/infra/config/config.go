// Package config reads command line settings.
package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Port        int
	DataDir     string
	Interface   string // preferred network interface name; empty = automatic
	RoomID      string
	RoomName    string
	MaxFailures int
	Lockout     time.Duration
	SessionTTL  time.Duration
}

// Load parses args (without the program name).
func Load(args []string, out io.Writer) (Config, error) {
	home, _ := os.UserHomeDir()
	cfg := Config{}
	fs := flag.NewFlagSet("lanshare", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.IntVar(&cfg.Port, "port", 8080, "HTTP port")
	fs.StringVar(&cfg.DataDir, "data", filepath.Join(home, "LANShare"), "folder for shared files and state")
	fs.StringVar(&cfg.Interface, "iface", "", "network interface to advertise, e.g. en0 (default: automatic)")
	fs.StringVar(&cfg.RoomName, "room-name", "ห้องหลัก", "name of the default room")
	fs.IntVar(&cfg.MaxFailures, "max-pin-failures", 5, "wrong PINs allowed before a client is locked out")
	fs.DurationVar(&cfg.Lockout, "lockout", 5*time.Minute, "lockout duration after too many wrong PINs")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	cfg.RoomID = "main"
	cfg.SessionTTL = 7 * 24 * time.Hour
	if cfg.Port < 1 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("invalid -port %d", cfg.Port)
	}
	if cfg.MaxFailures < 1 {
		return Config{}, fmt.Errorf("-max-pin-failures must be at least 1")
	}
	return cfg, nil
}
