//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lockLibrary takes the library in dir for this app alone; one app opens
// a library at a time. The Electron app holds it while Chromium's
// SingletonLock in the folder names a live Electron process. This app
// holds trie.lock with flock for as long as the returned file stays open,
// and writes its process id in it for the Electron app to check in turn.
func lockLibrary(dir string) (*os.File, error) {
	if electronHolds(dir, procName) {
		return nil, errElectron
	}
	f, err := os.OpenFile(filepath.Join(dir, "trie.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errHeld
		}
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// electronHolds reports whether the Electron app has the library in dir
// open. Chromium's SingletonLock there links to "host-pid" while it runs,
// and is left behind by a crash, when that process is gone or is another
// program.
func electronHolds(dir string, name func(pid int) string) bool {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(target[strings.LastIndexByte(target, '-')+1:])
	if err != nil || pid <= 0 || pid == os.Getpid() || !alive(pid) {
		return false
	}
	n := strings.ToLower(name(pid))
	return strings.Contains(n, "electron")
}

// alive reports whether a process with this id runs.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// procName is the program a process runs, as ps names it.
func procName(pid int) string {
	out, _ := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	return strings.TrimSpace(string(out))
}
