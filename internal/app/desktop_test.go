package app

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"ruilian/internal/licensecode"
)

func TestDefaultAdminPasswordChangeAndRestartInitialization(t *testing.T) {
	a, e := Open(t.TempDir(), licensecode.PublicKeyPEM, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if e = a.EnsureDefaultAccount(); e != nil {
		t.Fatal(e)
	}
	_, u, e := a.Login("huxinkui", DefaultAdminPassword, "test")
	if e != nil || u.Username != "huxinkui" {
		t.Fatalf("default login: %v", e)
	}
	if e = a.ChangePassword(u, "test", DefaultAdminPassword, "Changed-2026"); e != nil {
		t.Fatal(e)
	}
	if e = a.EnsureDefaultAccount(); e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.Login("huxinkui", DefaultAdminPassword, "test"); e == nil {
		t.Fatal("startup reset a changed password")
	}
	if _, _, e = a.Login("huxinkui", "Changed-2026", "test"); e != nil {
		t.Fatal(e)
	}
	var users int
	if e = a.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); e != nil || users != 1 {
		t.Fatalf("users = %d, %v", users, e)
	}
}

func TestDesktopLifecycleRequiresPrivateSessionToken(t *testing.T) {
	f := newFixture(t)
	f.a.DesktopToken = strings.Repeat("a", 64)
	stopped := make(chan struct{})
	f.a.Stop = func() { close(stopped) }
	router := f.a.Router(fstest.MapFS{"index.html": {Data: []byte("test")}})
	for _, token := range []string{"", "incorrect", f.a.DesktopToken} {
		request := httptest.NewRequest("POST", "/api/desktop/shutdown", bytes.NewBufferString("{}"))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Ruilian-Desktop-Token", token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if token != f.a.DesktopToken && response.Code != 403 {
			t.Fatalf("unauthorized status %d", response.Code)
		}
		if token == f.a.DesktopToken && response.Code != 200 {
			t.Fatalf("authorized status %d", response.Code)
		}
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("desktop shutdown did not stop service")
	}
}
