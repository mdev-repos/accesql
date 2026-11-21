package web

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"accesql/internal/session"
)

func get(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestStartServesAndShutsDown(t *testing.T) {
	sess := session.New()
	defer sess.Close()
	run, err := Start(sess, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Loopback || !strings.HasPrefix(run.URL, "http://127.0.0.1:") || !strings.Contains(run.URL, "/#t=") {
		t.Fatalf("URL inesperada: %s", run.URL)
	}
	base, token, _ := strings.Cut(run.URL, "/#t=")
	if len(token) != 32 {
		t.Errorf("el token debería tener 128 bits en hexadecimal: %q", token)
	}

	if code := get(t, base+"/", ""); code != 200 {
		t.Errorf("la página debería servirse sin token: %d", code)
	}
	if code := get(t, base+"/api/state", ""); code != http.StatusUnauthorized {
		t.Errorf("la API sin token: %d", code)
	}
	if code := get(t, base+"/api/state", token); code != 200 {
		t.Errorf("la API con token: %d", code)
	}

	run.Shutdown()
	select {
	case <-run.Done():
	default:
		t.Error("Done debería estar cerrado tras Shutdown")
	}
	if run.Err() != nil {
		t.Errorf("un apagado normal no es un error: %v", run.Err())
	}
	if _, err := http.Get(base + "/"); err == nil {
		t.Error("tras Shutdown el puerto debería estar cerrado")
	}
}

func TestStartEachTimeNewToken(t *testing.T) {
	sess := session.New()
	defer sess.Close()
	a, err := Start(sess, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown()
	b, err := Start(sess, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Shutdown()
	if a.URL == b.URL {
		t.Error("cada arranque debe tener su propio token y puerto")
	}
}

func TestStartPortInUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	sess := session.New()
	defer sess.Close()
	if _, err := Start(sess, "127.0.0.1", port); err == nil || !strings.Contains(err.Error(), "puerto") {
		t.Errorf("un puerto ocupado debe fallar con un mensaje claro: %v", err)
	}
}

func TestStartNonLoopbackIsFlagged(t *testing.T) {
	sess := session.New()
	defer sess.Close()
	run, err := Start(sess, "0.0.0.0", 0)
	if err != nil {
		t.Skip("no se puede escuchar en 0.0.0.0")
	}
	defer run.Shutdown()
	if run.Loopback {
		t.Error("0.0.0.0 no es loopback: la interfaz debe poder avisar que no hay cifrado")
	}
	if !strings.HasPrefix(run.URL, "http://127.0.0.1:") {
		t.Errorf("el enlace mostrado debe usar 127.0.0.1: %s", run.URL)
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{"127.0.0.1": true, "localhost": true, "::1": true, "0.0.0.0": false, "192.168.1.5": false, "example.com": false} {
		if isLoopback(host) != want {
			t.Errorf("isLoopback(%q) != %v", host, want)
		}
	}
}
