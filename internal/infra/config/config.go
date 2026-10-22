// Package config reads command line settings.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Defaults and bounds for the Phase C flags. The bounds are enforced when the
// flag value is set, so an out-of-range value never reaches Config.
const (
	defaultMinFree      int64 = 1 << 30
	defaultUploadTTL          = 24 * time.Hour
	minUploadTTL              = time.Minute
	defaultStallTimeout       = 30 * time.Second
	minStallTimeout           = 5 * time.Second
)

// Config holds the effective settings after Load has validated them.
type Config struct {
	Port        int
	DataDir     string
	Interface   string // preferred network interface name; empty = automatic
	RoomID      string
	RoomName    string
	MaxFailures int
	Lockout     time.Duration
	SessionTTL  time.Duration

	MinFree      int64         // bytes of free disk to keep in reserve; 0 = no reserve
	UploadTTL    time.Duration // how long an idle resumable upload is kept; >= 1m
	StallTimeout time.Duration // no-data time after which an upload request is cut; >= 5s
	KeepAwake    bool          // hold a caffeinate assertion while transfers are active
	// SystemPreview lets macOS tools (sips, Quick Look) draw previews of HEIC, camera RAW,
	// PDF, Office files and the like. They parse files from other devices, so the Owner can turn it off.
	SystemPreview bool
	// TLS serves HTTPS with a self-signed certificate in the data directory.
	TLS bool
}

// flagDoc is one line of the usage text. The usage is written by hand because
// flag.PrintDefaults cannot show a type for custom flag.Value types.
type flagDoc struct {
	name, typ, def, desc string
}

// Load parses args (without the program name).
//
// The FlagSet itself never writes anything: a parse failure yields exactly one
// error, returned to the caller. Usage text is written to out only for -h and
// -help (the error is then flag.ErrHelp) and for a flag that is undefined or
// has no value. Load touches neither the network nor the file system, so
// callers can rely on a failure happening before the port is bound and before
// the data directory is created.
func Load(args []string, out io.Writer) (Config, error) {
	home, _ := os.UserHomeDir()
	cfg := Config{
		MinFree:       defaultMinFree,
		UploadTTL:     defaultUploadTTL,
		StallTimeout:  defaultStallTimeout,
		KeepAwake:     true,
		SystemPreview: true,
	}
	var docs []flagDoc

	fs := flag.NewFlagSet("lanshare", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	defData := filepath.Join(home, "LANShare")
	fs.IntVar(&cfg.Port, "port", 8080, "HTTP port")
	docs = append(docs, flagDoc{"port", "int", "8080", "HTTP port, 1 to 65535"})
	fs.StringVar(&cfg.DataDir, "data", defData, "folder for shared files and state")
	docs = append(docs, flagDoc{"data", "string", fmt.Sprintf("%q", defData), "folder for shared files and state"})
	fs.StringVar(&cfg.Interface, "iface", "", "network interface to advertise, e.g. en0 (default: automatic)")
	docs = append(docs, flagDoc{"iface", "string", `""`, "network interface to advertise, e.g. en0 (empty: automatic)"})
	fs.StringVar(&cfg.RoomName, "room-name", "ห้องหลัก", "name of the default room")
	docs = append(docs, flagDoc{"room-name", "string", `"ห้องหลัก"`, "name of the default room"})
	fs.IntVar(&cfg.MaxFailures, "max-pin-failures", 5, "wrong PINs allowed before a client is locked out")
	docs = append(docs, flagDoc{"max-pin-failures", "int", "5", "wrong PINs allowed before a client is locked out, at least 1"})
	fs.DurationVar(&cfg.Lockout, "lockout", 5*time.Minute, "lockout duration after too many wrong PINs")
	docs = append(docs, flagDoc{"lockout", "duration", "5m0s", "lockout duration after too many wrong PINs"})

	minFreeDesc := "free disk space kept in reserve after an upload is accepted; " +
		"size as <digits><B|KiB|MiB|GiB>, 0B to 9223372036854775807B, 0B = no reserve"
	fs.Var((*sizeValue)(&cfg.MinFree), "min-free", minFreeDesc)
	docs = append(docs, flagDoc{"min-free", "size", FormatSize(defaultMinFree), minFreeDesc})

	uploadTTLDesc := "how long an unfinished resumable upload is kept; Go duration, at least 1m"
	fs.Var(&durationValue{p: &cfg.UploadTTL, min: minUploadTTL, minText: "1m"}, "upload-ttl", uploadTTLDesc)
	docs = append(docs, flagDoc{"upload-ttl", "duration", defaultUploadTTL.String(), uploadTTLDesc})

	stallDesc := "cut an upload request that receives no data for this long; Go duration, at least 5s"
	fs.Var(&durationValue{p: &cfg.StallTimeout, min: minStallTimeout, minText: "5s"}, "stall-timeout", stallDesc)
	docs = append(docs, flagDoc{"stall-timeout", "duration", defaultStallTimeout.String(), stallDesc})

	keepAwakeDesc := "keep the Mac awake while transfers are active; true or false"
	fs.Var((*strictBoolValue)(&cfg.KeepAwake), "keep-awake", keepAwakeDesc)
	docs = append(docs, flagDoc{"keep-awake", "bool", "true", keepAwakeDesc})

	sysPreviewDesc := "draw previews of HEIC, camera RAW, PDF, Office and text files with macOS sips and Quick Look; true or false"
	fs.Var((*strictBoolValue)(&cfg.SystemPreview), "system-preview", sysPreviewDesc)
	docs = append(docs, flagDoc{"system-preview", "bool", "true", sysPreviewDesc})

	tlsDesc := "serve HTTPS with a self-signed certificate stored in the data directory; true or false"
	fs.Var((*strictBoolValue)(&cfg.TLS), "tls", tlsDesc)
	docs = append(docs, flagDoc{"tls", "bool", "false", tlsDesc})

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) || isUsageError(err) {
			writeUsage(out, docs)
		}
		return Config{}, err
	}
	if fs.NArg() > 0 {
		// A stray word is most often a boolean flag written as "-keep-awake false",
		// which the flag package would silently treat as the end of the flags.
		writeUsage(out, docs)
		return Config{}, fmt.Errorf("unexpected argument %q (write boolean flags as -name=value)", fs.Arg(0))
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

// isUsageError reports whether err is the flag package's complaint about an
// undefined flag, a missing value or broken syntax, as opposed to a value that
// was rejected (which already carries its own range text).
func isUsageError(err error) bool {
	msg := err.Error()
	for _, p := range []string{"flag provided but not defined", "flag needs an argument", "bad flag syntax"} {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

func writeUsage(w io.Writer, docs []flagDoc) {
	var b strings.Builder
	b.WriteString("Usage: lanshare [flags]\n\nFlags (-name value or -name=value):\n")
	for _, d := range docs {
		fmt.Fprintf(&b, "  -%s %s\n        %s (default %s)\n", d.name, d.typ, d.desc, d.def)
	}
	_, _ = io.WriteString(w, b.String())
}

// sizeValue is a flag.Value holding a byte count parsed by ParseSize.
type sizeValue int64

func (v *sizeValue) Set(s string) error {
	n, err := ParseSize(s)
	if err != nil {
		return err
	}
	*v = sizeValue(n)
	return nil
}

func (v *sizeValue) String() string {
	if v == nil {
		return ""
	}
	return FormatSize(int64(*v))
}

// durationValue is a flag.Value for a Go duration with a lower bound.
type durationValue struct {
	p       *time.Duration
	min     time.Duration
	minText string // how the bound is written in messages, e.g. "1m"
}

func (v *durationValue) Set(s string) error {
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%q is not a Go duration such as 90s, 45m or 24h; accepted: a duration of at least %s", s, v.minText)
	}
	if d < v.min {
		return fmt.Errorf("%q is too short; accepted: a duration of at least %s", s, v.minText)
	}
	*v.p = d
	return nil
}

func (v *durationValue) String() string {
	if v == nil || v.p == nil {
		return ""
	}
	return v.p.String()
}

// strictBoolValue accepts exactly "true" or "false" (the flag package's own bool
// also takes 1, t, T, ...). A bare -name means true.
type strictBoolValue bool

func (v *strictBoolValue) Set(s string) error {
	switch s {
	case "true":
		*v = true
	case "false":
		*v = false
	default:
		return fmt.Errorf("%q is not a boolean; accepted: true or false", s)
	}
	return nil
}

func (v *strictBoolValue) String() string {
	if v == nil {
		return ""
	}
	if *v {
		return "true"
	}
	return "false"
}

func (v *strictBoolValue) IsBoolFlag() bool { return true }
