//go:build unix

package app

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"time"
)

func (s *Server) listenSuiteLocal() (net.Listener, func(), error) {
	if err := validateSuiteSocketDirectory(s.cfg.SuiteSocketDir); err != nil {
		return nil, nil, err
	}
	path, err := s.suiteSocketPath(suiteBackendOrigin)
	if err != nil {
		return nil, nil, err
	}
	fd, err := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("open Suite listener lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	release := func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = lock.Close() }
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, nil, fmt.Errorf("another Backend owns the local Suite listener")
	}
	if info, e := os.Lstat(path); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			release()
			return nil, nil, fmt.Errorf("refuse to replace a non-socket Suite path")
		}
		conn, e := net.DialTimeout("unix", path, 100*time.Millisecond)
		if e == nil {
			_ = conn.Close()
			release()
			return nil, nil, fmt.Errorf("Suite socket already has a listener")
		}
		if !errors.Is(e, unix.ECONNREFUSED) && !errors.Is(e, os.ErrNotExist) {
			release()
			return nil, nil, fmt.Errorf("existing Suite listener could not be safely classified: %w", e)
		}
		if err = os.Remove(path); err != nil {
			release()
			return nil, nil, err
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		release()
		return nil, nil, e
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		release()
		return nil, nil, err
	}
	listener.SetUnlinkOnClose(false)
	own, err := os.Lstat(path)
	cleanup := func() {
		_ = listener.Close()
		current, e := os.Lstat(path)
		if e == nil && own != nil && os.SameFile(own, current) {
			_ = os.Remove(path)
		}
		release()
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err = os.Chmod(path, 0660); err != nil {
		cleanup()
		return nil, nil, err
	}
	return listener, cleanup, nil
}
