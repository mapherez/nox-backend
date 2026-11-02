//go:build unix

package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func shortSuiteDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "suite-local-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
func TestLocalListenerOwnershipStaleRecoveryAndSafeCleanup(t *testing.T) {
	dir := shortSuiteDirectory(t)
	s := &Server{cfg: Config{SuiteSocketDir: dir}}
	_, cleanup, err := s.listenSuiteLocal()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.listenSuiteLocal(); err == nil {
		t.Fatal("second listener replaced active owner")
	}
	cleanup()
	path := filepath.Join(dir, "nox-backend.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	_ = stale.Close()
	_, cleanup, err = s.listenSuiteLocal()
	if err != nil {
		t.Fatal("stale socket could not be recovered", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("do not remove"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "do not remove" {
		t.Fatal("cleanup removed a replacement file", err)
	}
	if _, _, err = s.listenSuiteLocal(); err == nil {
		t.Fatal("ordinary file overwritten by listener")
	}
}
func TestLocalChannelRejectsNetworkTargetsAndFilesystemSubstitutions(t *testing.T) {
	dir := shortSuiteDirectory(t)
	s := &Server{cfg: Config{SuiteSocketDir: dir}}
	for _, origin := range []string{"https://nox-yard", "http://127.0.0.1", "http://192.168.10.10:8095", "https://public.example.com", "http://nox-yard:8080", "http://nox-yard/../../outside", "http://nox-yard?next=secret"} {
		if _, err := s.suiteSocketPath(origin); err == nil {
			t.Fatal("network or invalid origin accepted", origin)
		}
	}
	outside := filepath.Join(dir, "ordinary-file")
	if err := os.WriteFile(outside, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "nox-yard.sock")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.fetchSuiteDeclaration(context.Background(), "http://nox-yard", ""); err == nil {
		t.Fatal("symlink accepted as participant")
	}
	unsafeDir := filepath.Join(dir, "unsafe")
	if err := os.Mkdir(unsafeDir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeDir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := validateSuiteSocketDirectory(unsafeDir); err == nil {
		t.Fatal("world-accessible directory accepted")
	}
}
func TestBackendRunMaintainsNativeHTTPAndClosesLocalListener(t *testing.T) {
	dir := shortSuiteDirectory(t)
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reservation.Addr().String()
	_ = reservation.Close()
	cfg := Config{Addr: addr, DataDir: t.TempDir(), SuiteSocketDir: dir, Version: "local-test"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- Run(ctx, cfg) }()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, e := client.Get("http://" + addr + "/v1/health")
		if e == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		select {
		case e := <-stopped:
			t.Fatal("Backend stopped during startup", e)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("native HTTP did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := &Server{cfg: cfg}
	local, closeLocal, err := s.suiteLocalClient(suiteBackendOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLocal()
	res, err := local.Get(suiteBackendOrigin + "/api/nox/keys")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("local listener not available", res.StatusCode)
	}
	res, err = client.Get("http://" + addr + "/api/nox/keys")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("network transport allowed")
	}
	cancel()
	select {
	case err = <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if _, err = os.Lstat(filepath.Join(dir, "nox-backend.sock")); !os.IsNotExist(err) {
		t.Fatal("owned socket left after shutdown", err)
	}
}

func TestLocalOriginSupportsEveryProductIDShape(t *testing.T) {
	s := &Server{cfg: Config{SuiteSocketDir: shortSuiteDirectory(t)}}
	for _, id := range []string{"nox-yard", "0", "1234567890", strings.Repeat("a", 64), strings.Repeat("1", 64)} {
		origin := suiteProductOrigin(id)
		path, err := s.suiteSocketPath(origin)
		if err != nil || filepath.Base(path) != id+".sock" {
			t.Fatal("valid product identity lost its local mapping", id, err)
		}
	}
}
