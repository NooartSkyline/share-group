//go:build ignore

// Command bench serves the web/ folder over plain HTTP so a phone can open
// web/test/bench-sha256.html and measure SHA-256 speed.
//
// Run from the repository root: go run web/test/bench/main.go
//
// This is a temporary measuring tool, not part of LAN Share. It has NO
// authentication: while it runs, anyone on the LAN can fetch every static file
// under web/. Stop it (Ctrl+C) as soon as the measurement is done.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lanshare/internal/adapter/netinfo"
)

const benchPage = "/test/bench-sha256.html"

func main() {
	port := flag.Int("port", 8090, "TCP port to listen on (all interfaces)")
	dir := flag.String("dir", "web", "folder to serve (must contain sha256.js and hash-core.js)")
	flag.Parse()

	if err := run(*dir, *port); err != nil {
		log.Fatal(err)
	}
}

func run(dir string, port int) error {
	for _, f := range []string{"sha256.js", "hash-core.js", filepath.Join("test", "bench-sha256.html")} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return fmt.Errorf("folder %q is missing %s (run from the repository root or pass -dir): %w", dir, f, err)
		}
	}

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", port, err)
	}
	srv := &http.Server{
		Handler:           handler(dir),
		ReadHeaderTimeout: 10 * time.Second,
	}

	printBanner(port)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	fmt.Println("\nstopping bench server")
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// handler serves dir read-only. The bench page is outside the main app, so it
// carries its own CSP: same-origin scripts and workers only, no inline script.
func handler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/" {
			http.Redirect(w, r, benchPage, http.StatusFound)
			return
		}
		// No directory listings.
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; worker-src 'self'; style-src 'self'; connect-src 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}

func printBanner(port int) {
	fmt.Println("==================================================================")
	fmt.Println("คำเตือน: เซิร์ฟเวอร์วัดความเร็วนี้ไม่มีการยืนยันตัวตน")
	fmt.Println("ใครก็ตามในเครือข่าย LAN เดียวกันดึงไฟล์ static ทั้งหมดในโฟลเดอร์ web/ ได้")
	fmt.Println("ใช้ชั่วคราวระหว่างวัดเท่านั้น วัดเสร็จให้กด Ctrl+C ปิดทันที")
	fmt.Println("(ข้อมูลส่งเป็น HTTP ธรรมดา และไม่เกี่ยวกับห้อง/PIN ของ LAN Share)")
	fmt.Println("==================================================================")
	fmt.Printf("\nเปิดบนมือถือที่อยู่ใน Wi-Fi เดียวกัน:\n")

	ifaces, err := netinfo.New().Interfaces(context.Background())
	if err != nil || len(ifaces) == 0 {
		fmt.Printf("  (หา IP ของเครื่องไม่ได้) http://<IP ของ Mac>:%d%s\n", port, benchPage)
	}
	for _, ifc := range ifaces {
		note := ""
		if ifc.Kind != "physical" {
			note = "  [" + string(ifc.Kind) + " อาจเข้าจากมือถือไม่ได้]"
		}
		fmt.Printf("  http://%s:%d%s   (%s)%s\n", ifc.IP, port, benchPage, ifc.Name, note)
	}
	fmt.Printf("\nบน Mac เอง: http://localhost:%d%s\n", port, benchPage)
	fmt.Println("กด Ctrl+C เพื่อหยุด")
}
