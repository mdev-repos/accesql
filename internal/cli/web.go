package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"access2mysql/internal/session"
	"access2mysql/internal/web"

	"github.com/spf13/cobra"
)

func newWebCmd() *cobra.Command {
	var (
		file string
		host string
		port int
		open bool
	)

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Abre la interfaz gráfica en el navegador",
		Long: `Levanta una interfaz web local: abrir una base de Access, generar la imagen
MySQL, exponerla como servidor MySQL, consultar con SQL, exportar y convertir a
scripts MySQL. No necesita internet ni instalar nada: la página va dentro del
ejecutable.

El archivo se elige desde la propia página; con -f queda preseleccionado.

Por seguridad escucha solo en 127.0.0.1, exige un token aleatorio (va en el
enlace que se imprime al iniciar) y la imagen es de solo lectura.`,
		Example: `  access-tool web --open
  access-tool web -f datos.accdb --open`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := session.New()
			defer sess.Close()
			if file != "" {
				if err := sess.Open(file); err != nil {
					return err
				}
			}

			token, err := newToken()
			if err != nil {
				return err
			}

			// Escuchamos antes de nada más para fallar rápido si el puerto está ocupado.
			ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
			if err != nil {
				return fmt.Errorf("no se pudo escuchar en %s:%d (¿puerto ocupado? probar con --port): %w", host, port, err)
			}
			actualPort := ln.Addr().(*net.TCPAddr).Port

			loopback := isLoopback(host)
			handler, err := web.New(sess, web.Options{Token: token, Port: actualPort, StrictHost: loopback})
			if err != nil {
				return err
			}
			srv := &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       30 * time.Second,
				IdleTimeout:       60 * time.Second,
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			stderr := cmd.ErrOrStderr()
			// El token va en el fragmento (#): el navegador no lo envía al
			// servidor ni lo incluye en el Referer.
			shownHost := host
			if host == "0.0.0.0" || host == "::" {
				shownHost = "127.0.0.1"
			}
			url := fmt.Sprintf("http://%s:%d/#t=%s", shownHost, actualPort, token)
			fmt.Fprintf(stderr, "\nInterfaz lista. Abrí este enlace en tu navegador:\n\n    %s\n\nCtrl+C para detener.\n", url)
			if !loopback {
				fmt.Fprintf(stderr, "\nADVERTENCIA: escuchando en %s sin cifrado (HTTP). El token viaja en claro y quien lo tenga puede explorar carpetas y abrir un puerto MySQL: usar solo en redes de confianza o detrás de un túnel SSH/VPN.\n", host)
			}
			if open {
				if err := openBrowser(url); err != nil {
					fmt.Fprintf(stderr, "no se pudo abrir el navegador automáticamente: %v\n", err)
				}
			}

			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				srv.Shutdown(shutdownCtx)
			}()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "archivo .accdb/.mdb a preseleccionar (opcional: se puede elegir desde la página)")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "dirección donde escuchar (0.0.0.0 expone a toda la red, sin cifrado: usar con cuidado)")
	cmd.Flags().IntVarP(&port, "port", "p", 8080, "puerto HTTP (0 elige uno libre)")
	cmd.Flags().BoolVar(&open, "open", false, "abrir el navegador automáticamente")
	return cmd
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

// openBrowser abre la URL con el navegador predeterminado del sistema.
func openBrowser(url string) error {
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
