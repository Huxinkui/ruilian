//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

func Lock(dataDir string) (func(), error) {
	if e := os.MkdirAll(dataDir, 0700); e != nil {
		return nil, e
	}
	p, e := syscall.UTF16PtrFromString(filepath.Join(dataDir, ".ruilian.lock"))
	if e != nil {
		return nil, e
	}
	handle, e := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, fmt.Errorf("当前数据目录已被其他客户端进程使用：%w", e)
	}
	return func() { _ = syscall.CloseHandle(handle) }, nil
}
func Browser(address string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", address)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}
func Message(title, message string) {
	dll := syscall.NewLazyDLL("user32.dll")
	proc := dll.NewProc("MessageBoxW")
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(message)
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x00000040)
}
