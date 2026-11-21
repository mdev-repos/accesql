package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"accesql/internal/session"
	"accesql/internal/web"

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
		Example: `  accesql web --open
  accesql web -f datos.accdb --open`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := session.New()
			defer sess.Close()
			if file != "" {
				if err := sess.Open(file); err != nil {
					return err
				}
			}

			run, err := web.Start(sess, host, port)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			stderr := cmd.ErrOrStderr()
			fmt.Fprintf(stderr, "\nInterfaz lista. Abrí este enlace en tu navegador:\n\n    %s\n\nCtrl+C para detener.\n", run.URL)
			if !run.Loopback {
				fmt.Fprintf(stderr, "\nADVERTENCIA: escuchando en %s sin cifrado (HTTP). El token viaja en claro y quien lo tenga puede explorar carpetas y abrir un puerto MySQL: usar solo en redes de confianza o detrás de un túnel SSH/VPN.\n", host)
			}
			if open {
				if err := web.OpenBrowser(run.URL); err != nil {
					fmt.Fprintf(stderr, "no se pudo abrir el navegador automáticamente: %v\n", err)
				}
			}

			select {
			case <-ctx.Done():
				run.Shutdown()
			case <-run.Done():
			}
			return run.Err()
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "archivo .accdb/.mdb a preseleccionar (opcional: se puede elegir desde la página)")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "dirección donde escuchar (0.0.0.0 expone a toda la red, sin cifrado: usar con cuidado)")
	cmd.Flags().IntVarP(&port, "port", "p", 8080, "puerto HTTP (0 elige uno libre)")
	cmd.Flags().BoolVar(&open, "open", false, "abrir el navegador automáticamente")
	return cmd
}
