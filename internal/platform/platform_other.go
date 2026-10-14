//go:build !windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func Lock(dataDir string) (func(), error) {
	if e := os.MkdirAll(dataDir, 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(dataDir, ".ruilian.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
func Browser(address string) error  { return exec.Command("xdg-open", address).Start() }
func Message(title, message string) { fmt.Fprintln(os.Stderr, title+": "+message) }
