//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmDestroy      = 0x0002
	wmCommand      = 0x0111
	wmRButtonUp    = 0x0205
	wmLButtonUp    = 0x0202
	wmApp          = 0x8000
	wmTray         = wmApp + 1
	nimAdd         = 0
	nimModify      = 1
	nimDelete      = 2
	nifMessage     = 0x00000001
	nifIcon        = 0x00000002
	nifTip         = 0x00000004
	idiApplication = 32512
	mfString       = 0x00000000
	mfGrayed       = 0x00000001
	mfSeparator    = 0x00000800
	tpmRightButton = 0x0002
	wsPopup        = 0x80000000
	wsExToolwindow = 0x00000080
	swShowNormal   = 1
	idRoom         = 1
	idPIN          = 2
	idURL          = 3
	idNIC          = 4
	idDevices      = 5
	idPending      = 6
	idOpen         = 10
	idToggle       = 11
	idExit         = 12
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	shell32           = syscall.NewLazyDLL("shell32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procRegisterClass = user32.NewProc("RegisterClassExW")
	procCreateWindow  = user32.NewProc("CreateWindowExW")
	procDefWindowProc = user32.NewProc("DefWindowProcW")
	procGetMessage    = user32.NewProc("GetMessageW")
	procTranslate     = user32.NewProc("TranslateMessage")
	procDispatch      = user32.NewProc("DispatchMessageW")
	procPostQuit      = user32.NewProc("PostQuitMessage")
	procDestroyWindow = user32.NewProc("DestroyWindow")
	procLoadIcon      = user32.NewProc("LoadIconW")
	procCreatePopup   = user32.NewProc("CreatePopupMenu")
	procAppendMenu    = user32.NewProc("AppendMenuW")
	procTrackPopup    = user32.NewProc("TrackPopupMenu")
	procDestroyMenu   = user32.NewProc("DestroyMenu")
	procGetCursor     = user32.NewProc("GetCursorPos")
	procSetForeground = user32.NewProc("SetForegroundWindow")
	procPostMessage   = user32.NewProc("PostMessageW")
	procShellNotify   = shell32.NewProc("Shell_NotifyIconW")
	procShellExecute  = shell32.NewProc("ShellExecuteW")
	procGetModule     = kernel32.NewProc("GetModuleHandleW")

	trayApp    *app
	trayWnd    uintptr
	wndProcPtr = syscall.NewCallback(trayWndProc)
	notifySize = uint32(unsafe.Sizeof(notifyIconData{}))
)

type notifyIconData struct {
	Size            uint32
	_               uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	_               uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Timeout         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type point struct{ X, Y int32 }

type msg struct {
	Wnd     uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	_       uint32
}

func runTray(a *app) error {
	trayApp = a
	inst, _, _ := procGetModule.Call(0)
	className, err := syscall.UTF16PtrFromString("ShareGroupTray")
	if err != nil {
		return err
	}
	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   wndProcPtr,
		Instance:  inst,
		ClassName: className,
	}
	if r, _, e := procRegisterClass.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("register window class: %w", e)
	}
	title, _ := syscall.UTF16PtrFromString("Share Group")
	hwnd, _, e := procCreateWindow.Call(
		wsExToolwindow, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsPopup, 0, 0, 0, 0, 0, 0, inst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("create tray window: %w", e)
	}
	trayWnd = hwnd
	if err := addTrayIcon(hwnd); err != nil {
		return err
	}
	go func() {
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for range t.C {
			a.refresh()
			updateTip(hwnd)
		}
	}()
	a.refresh()
	updateTip(hwnd)

	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslate.Call(uintptr(unsafe.Pointer(&m)))
		procDispatch.Call(uintptr(unsafe.Pointer(&m)))
	}
	a.stopServer()
	return nil
}

func trayWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmTray:
		if lParam == wmRButtonUp || lParam == wmLButtonUp {
			showMenu(hwnd)
		}
		return 0
	case wmCommand:
		handleMenu(uint16(wParam))
		return 0
	case wmDestroy:
		_ = notify(nimDelete, hwnd, "")
		procPostQuit.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return r
}

func handleMenu(id uint16) {
	if trayApp == nil {
		return
	}
	switch id {
	case idPIN:
		trayApp.copyPIN()
	case idURL:
		trayApp.copyURL()
	case idOpen:
		trayApp.openDashboard()
	case idToggle:
		trayApp.toggleServer()
		time.AfterFunc(startWait, func() {
			trayApp.refresh()
			if trayWnd != 0 {
				updateTip(trayWnd)
			}
		})
	case idExit:
		procDestroyWindow.Call(trayWnd)
	}
}

func showMenu(hwnd uintptr) {
	if trayApp == nil {
		return
	}
	trayApp.refresh()
	inf, running := trayApp.snapshot()
	menu, _, _ := procCreatePopup.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	room := "ห้อง: —"
	if inf.roomName != "" {
		room = "ห้อง: " + inf.roomName
	}
	pin := "PIN: —"
	pinFlags := uint32(mfString | mfGrayed)
	if inf.pin != "" {
		pin = "PIN: " + inf.pin + "  (คัดลอก)"
		pinFlags = mfString
	}
	nic := "เครือข่าย: —"
	if inf.iface != "" {
		nic = "เครือข่าย: " + inf.iface
	}
	appendMenu(menu, mfString|mfGrayed, idRoom, room)
	appendMenu(menu, pinFlags, idPIN, pin)
	urlFlags := uint32(mfString)
	if inf.url == "" {
		urlFlags |= mfGrayed
	}
	appendMenu(menu, urlFlags, idURL, "คัดลอก URL")
	appendMenu(menu, mfString|mfGrayed, idNIC, nic)
	appendMenu(menu, mfString|mfGrayed, idDevices, fmt.Sprintf("อุปกรณ์: %d", inf.deviceCount))
	if inf.pendingCount > 0 {
		appendMenu(menu, mfString|mfGrayed, idPending, fmt.Sprintf("รออนุมัติ: %d", inf.pendingCount))
	}
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, idOpen, "เปิดโปรแกรม")
	toggle := "เปิดเซิร์ฟเวอร์"
	if running {
		toggle = "หยุดเซิร์ฟเวอร์"
	}
	appendMenu(menu, mfString, idToggle, toggle)
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, idExit, "ออก")

	var pt point
	procGetCursor.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForeground.Call(hwnd)
	procTrackPopup.Call(menu, tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	procPostMessage.Call(hwnd, 0, 0, 0)
}

func appendMenu(menu uintptr, flags uint32, id uint16, title string) {
	p, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	procAppendMenu.Call(menu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(p)))
}

func addTrayIcon(hwnd uintptr) error {
	if err := notify(nimAdd, hwnd, "Share Group"); err != nil {
		return err
	}
	return nil
}

func updateTip(hwnd uintptr) {
	if trayApp == nil {
		return
	}
	inf, running := trayApp.snapshot()
	tip := "Share Group"
	if !running {
		tip = "Share Group — หยุดอยู่"
	} else if inf.roomName != "" {
		tip = "Share Group — " + inf.roomName
	}
	_ = notify(nimModify, hwnd, tip)
}

func notify(action uint32, hwnd uintptr, tip string) error {
	icon, _, _ := procLoadIcon.Call(0, uintptr(idiApplication))
	nid := notifyIconData{
		Size:            notifySize,
		Wnd:             hwnd,
		ID:              1,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: wmTray,
		Icon:            icon,
	}
	copyUTF16(nid.Tip[:], tip)
	r, _, e := procShellNotify.Call(uintptr(action), uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		return fmt.Errorf("tray icon: %w", e)
	}
	return nil
}

func copyUTF16(dst []uint16, s string) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	n := len(u)
	if n > len(dst) {
		n = len(dst)
		u[n-1] = 0
	}
	copy(dst, u[:n])
}

func openURL(raw string) {
	op, _ := syscall.UTF16PtrFromString("open")
	u, err := syscall.UTF16PtrFromString(raw)
	if err != nil {
		return
	}
	procShellExecute.Call(0, uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(u)), 0, 0, swShowNormal)
}

func copyText(s string) error {
	cmd := exec.Command("cmd", "/c", "clip")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
