//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	hkeyCurrentUser = 0x80000001
	keyWrite        = 0x20006
	regSz           = 1
	mbOK            = 0
	mbIconError     = 0x00000010
	mbIconInfo      = 0x00000040
	explorerKey     = `Software\Classes\*\shell\ShareGroupSend`
	explorerCmdKey  = `Software\Classes\*\shell\ShareGroupSend\command`
	explorerLabel   = "ส่งไป Share Group"
)

var (
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	procRegCreateKey  = advapi32.NewProc("RegCreateKeyExW")
	procRegSetValue   = advapi32.NewProc("RegSetValueExW")
	procRegCloseKey   = advapi32.NewProc("RegCloseKey")
	procRegDeleteTree = advapi32.NewProc("RegDeleteTreeW")
	procMessageBox    = user32.NewProc("MessageBoxW")
)

func installExplorer() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("หาตำแหน่งโปรแกรม: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	if shareBinary() == "" {
		return fmt.Errorf("ไม่พบ share.exe — วางไว้โฟลเดอร์เดียวกับ ShareGroup.exe แล้วติดตั้งอีกครั้ง")
	}
	cmd := `"` + exe + `" -send "%1"`
	if err := setRegSZ(explorerKey, "", explorerLabel); err != nil {
		return err
	}
	if err := setRegSZ(explorerKey, "Icon", exe+",0"); err != nil {
		return err
	}
	return setRegSZ(explorerCmdKey, "", cmd)
}

func uninstallExplorer() error {
	p, err := syscall.UTF16PtrFromString(explorerKey)
	if err != nil {
		return err
	}
	r, _, e := procRegDeleteTree.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(p)))
	if r != 0 && r != 2 { // ERROR_FILE_NOT_FOUND
		return fmt.Errorf("ถอดคลิกขวาไม่สำเร็จ: %w", e)
	}
	return nil
}

func sendFiles(files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("ไม่มีไฟล์ที่จะส่ง")
	}
	bin := shareBinary()
	if bin == "" {
		return fmt.Errorf("ไม่พบ share.exe — วางไว้โฟลเดอร์เดียวกับ ShareGroup.exe")
	}
	args := append([]string{"send"}, files...)
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func setRegSZ(path, name, value string) error {
	sub, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var h uintptr
	r, _, e := procRegCreateKey.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(sub)),
		0, 0, 0, keyWrite, 0,
		uintptr(unsafe.Pointer(&h)),
		0,
	)
	if r != 0 {
		return fmt.Errorf("เขียนรีจิสทรี: %w", e)
	}
	defer procRegCloseKey.Call(h)

	var namePtr uintptr
	if name != "" {
		np, err := syscall.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		namePtr = uintptr(unsafe.Pointer(np))
	}
	val, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	r, _, e = procRegSetValue.Call(
		h, namePtr, 0, regSz,
		uintptr(unsafe.Pointer(&val[0])),
		uintptr(len(val)*2),
	)
	if r != 0 {
		return fmt.Errorf("เขียนรีจิสทรี: %w", e)
	}
	return nil
}

func messageBox(title, text string, fail bool) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	b, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	flags := uintptr(mbOK | mbIconInfo)
	if fail {
		flags = mbOK | mbIconError
	}
	procMessageBox.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), flags)
}
