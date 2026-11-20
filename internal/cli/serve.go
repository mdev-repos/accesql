package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"access2mysql/internal/access"
	"access2mysql/internal/engine"

	"github.com/spf13/cobra"
)

// passwordEnv es la variable de entorno alternativa al flag --password. Es
// preferible porque los argumentos de un proceso son visibles para otros
// usuarios del sistema (ps), las variables de entorno no tanto.
const passwordEnv = "ACCESS_TOOL_PASSWORD"

func newServeCmd() *cobra.Command {
	var (
		file     string
		host     string
		port     int
		user     string
		password string
		database string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Expone el Access como un servidor MySQL (solo lectura)",
		Long: `Carga el archivo Access en memoria y lo sirve con el protocolo de MySQL.
Las aplicaciones (PHP con PDO/mysqli, etc.) se conectan con su driver de siempre.

Es una foto de solo lectura: las escrituras (INSERT/UPDATE/DELETE) se rechazan.
Si el .accdb cambia, reiniciar el servidor para recargarlo.

Por seguridad escucha solo en 127.0.0.1 y exige usuario y contraseña. La
contraseña se pasa con --password o, mejor, con la variable ` + passwordEnv + `.`,
		Example: `  ` + passwordEnv + `=secreta access-tool serve -f datos.accdb --port 3307
  mysql -h 127.0.0.1 -P 3307 -u access -psecreta access`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if password == "" {
				password = os.Getenv(passwordEnv)
			}
			if password == "" {
				return fmt.Errorf("falta la contraseña: usar --password o la variable %s", passwordEnv)
			}

			src, err := access.Open(file)
			if err != nil {
				return err
			}
			defer src.Close()

			fmt.Fprintf(cmd.ErrOrStderr(), "cargando %s ...\n", file)
			eng, err := engine.New(src, database)
			if err != nil {
				return err
			}
			// El motor ya tiene copia en memoria: el archivo no se vuelve a leer.
			src.Close()

			// Ctrl+C o SIGTERM cancelan el contexto y el servidor se apaga solo.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			addr := net.JoinHostPort(host, strconv.Itoa(port))
			fmt.Fprintf(cmd.ErrOrStderr(), "escuchando en %s (solo lectura), base %q, usuario %q. Ctrl+C para detener\n", addr, database, user)
			err = eng.Serve(ctx, engine.ServeOptions{Address: addr, User: user, Password: password})
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "archivo .accdb/.mdb (obligatorio)")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "dirección donde escuchar (0.0.0.0 expone a toda la red: usar con cuidado)")
	cmd.Flags().IntVarP(&port, "port", "p", 3306, "puerto TCP")
	cmd.Flags().StringVarP(&user, "user", "u", "access", "usuario para conectarse")
	cmd.Flags().StringVar(&password, "password", "", "contraseña (o variable "+passwordEnv+")")
	cmd.Flags().StringVarP(&database, "database", "d", engine.DefaultDatabase, "nombre de la base que ven los clientes")
	cmd.MarkFlagRequired("file")
	return cmd
}
