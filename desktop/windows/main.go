//go:build windows

// Command windows is the Windows tray shell. It starts and stops lanshare.exe
// and opens the same web dashboard on localhost. Room, PIN, and roles stay in
// the Go server; this process only talks to 127.0.0.1 like the Mac menu bar.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	defaultPort   = 8080
	pollInterval  = 2 * time.Second
	httpTimeout   = 1500 * time.Millisecond
	startWait     = time.Second
	dashboardWait = 400 * time.Millisecond
)

type info struct {
	ok           bool
	roomName     string
	pin          string
	url          string
	iface        string
	deviceCount  int
	pendingCount int
}

type app struct {
	port      int
	mu        sync.Mutex
	cmd       *exec.Cmd
	launching bool
	info      info
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-install":
			exitUI(installExplorer(), "ติดตั้งแล้ว: คลิกขวาไฟล์ใน Explorer แล้วเลือก \"ส่งไป Share Group\"\nเซิร์ฟเวอร์ Share Group ต้องเปิดอยู่บนเครื่องนี้")
		case "-uninstall":
			exitUI(uninstallExplorer(), "ถอดคลิกขวา Explorer แล้ว")
		case "-send":
			exitUI(sendFiles(os.Args[2:]), "")
		}
	}
	a := &app{port: defaultPort}
	a.startServerIfNeeded()
	time.Sleep(dashboardWait)
	a.openDashboard()
	if err := runTray(a); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func exitUI(err error, ok string) {
	if err != nil {
		messageBox("Share Group", err.Error(), true)
		os.Exit(1)
	}
	if ok != "" {
		messageBox("Share Group", ok, false)
	}
	os.Exit(0)
}

func (a *app) running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd != nil && a.cmd.Process != nil {
		return true
	}
	return a.info.ok
}

func (a *app) snapshot() (info, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	own := a.cmd != nil && a.cmd.Process != nil
	return a.info, own || a.info.ok
}

func (a *app) startServerIfNeeded() {
	a.mu.Lock()
	if a.cmd != nil || a.launching {
		a.mu.Unlock()
		return
	}
	bin := lanshareBinary()
	if bin == "" {
		a.mu.Unlock()
		return
	}
	cmd := exec.Command(bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	a.launching = true
	a.mu.Unlock()

	if err := cmd.Start(); err != nil {
		a.mu.Lock()
		a.launching = false
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	a.cmd = cmd
	a.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		a.mu.Lock()
		if a.cmd == cmd {
			a.cmd = nil
		}
		a.mu.Unlock()
	}()
	time.AfterFunc(startWait, func() {
		a.mu.Lock()
		a.launching = false
		a.mu.Unlock()
		a.refresh()
	})
}

func (a *app) stopServer() {
	a.mu.Lock()
	cmd := a.cmd
	a.cmd = nil
	a.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

func (a *app) toggleServer() {
	if a.running() {
		a.stopServer()
		return
	}
	a.startServerIfNeeded()
}

func (a *app) openDashboard() {
	a.startServerIfNeeded()
	url := fmt.Sprintf("http://127.0.0.1:%d/", a.port)
	openURL(url)
}

func (a *app) refresh() {
	next := info{}
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/shortcuts/info", a.port), nil)
	if err == nil {
		req.Header.Set("Cache-Control", "no-cache")
		if res, err := client.Do(req); err == nil {
			if res.StatusCode == http.StatusOK {
				var body struct {
					RoomName  string `json:"roomName"`
					PIN       string `json:"pin"`
					URL       string `json:"url"`
					Interface struct {
						Name string `json:"name"`
						IP   string `json:"ip"`
					} `json:"interface"`
					DeviceCount  int `json:"deviceCount"`
					PendingCount int `json:"pendingCount"`
				}
				if json.NewDecoder(res.Body).Decode(&body) == nil {
					next.ok = true
					next.roomName = body.RoomName
					next.pin = body.PIN
					next.url = body.URL
					next.iface = joinNonEmpty(" • ", body.Interface.Name, body.Interface.IP)
					next.deviceCount = body.DeviceCount
					next.pendingCount = body.PendingCount
				}
			}
			_ = res.Body.Close()
		}
	}
	a.mu.Lock()
	a.info = next
	a.mu.Unlock()
}

func (a *app) copyPIN() {
	a.mu.Lock()
	pin := a.info.pin
	a.mu.Unlock()
	if pin != "" {
		_ = copyText(pin)
	}
}

func (a *app) copyURL() {
	a.mu.Lock()
	u := a.info.url
	a.mu.Unlock()
	if u != "" {
		_ = copyText(u)
	}
}

func lanshareBinary() string {
	return findSibling("lanshare.exe", "lanshare")
}

func shareBinary() string {
	return findSibling("share.exe", "share")
}

func findSibling(names ...string) string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "LANShare", "bin"))
	}
	for _, dir := range dirs {
		for _, name := range names {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

func joinNonEmpty(sep string, parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += p
	}
	return out
}
