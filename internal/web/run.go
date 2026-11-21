package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"accesql/internal/session"
)

// Running es un servidor web en marcha. Lo crea Start y lo usan tanto el
// comando `web` como el menú de la terminal (que abre la interfaz gráfica
// sobre la misma sesión).
type Running struct {
	// URL es el enlace completo, con el token en el fragmento (#t=...).
	URL string
	// Loopback dice si escucha solo en esta computadora. Si no, el tráfico (y el
	// token) viajan sin cifrar por la red.
	Loopback bool

	srv  *http.Server
	done chan struct{}
	err  error
}

// Start abre el puerto (0 elige uno libre) y atiende en segundo plano. Falla
// enseguida si el puerto está ocupado.
func Start(sess *session.Session, host string, port int) (*Running, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	// Se escucha antes de nada más para fallar rápido si el puerto está ocupado.
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("no se pudo escuchar en %s:%d (¿puerto ocupado? probar con otro puerto): %w", host, port, err)
	}
	actual := ln.Addr().(*net.TCPAddr).Port

	loopback := isLoopback(host)
	handler, err := New(sess, Options{Token: token, Port: actual, StrictHost: loopback})
	if err != nil {
		ln.Close()
		return nil, err
	}
	r := &Running{
		Loopback: loopback,
		done:     make(chan struct{}),
		srv: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
	// El token va en el fragmento (#): el navegador no lo envía al servidor ni
	// lo incluye en el Referer.
	shown := host
	if host == "0.0.0.0" || host == "::" {
		shown = "127.0.0.1"
	}
	r.URL = fmt.Sprintf("http://%s:%d/#t=%s", shown, actual, token)

	go func() {
		if err := r.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.err = err
		}
		close(r.done)
	}()
	return r, nil
}

// Done se cierra cuando el servidor dejó de atender.
func (r *Running) Done() <-chan struct{} { return r.done }

// Err devuelve el error que lo detuvo (nil si fue por Shutdown). Solo tiene
// sentido después de que Done se cerró.
func (r *Running) Err() error { return r.err }

// Shutdown lo detiene, dejando terminar las peticiones en curso (hasta 5 s).
func (r *Running) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.srv.Shutdown(ctx)
	<-r.done
}

// newToken genera 128 bits aleatorios en hexadecimal.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// OpenBrowser abre la URL con el navegador predeterminado del sistema.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
