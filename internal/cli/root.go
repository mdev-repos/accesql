// Package cli define los comandos de línea de comandos (cobra).
package cli

import (
	"os"

	"accesql/internal/session"
	"accesql/internal/tui/app"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

// isTerminal dice si la entrada y la salida son una terminal de verdad (y no un
// pipe o un archivo, como cuando corre dentro de un script).
func isTerminal() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

func newRootCmd() *cobra.Command {
	var file string
	root := &cobra.Command{
		Use:   "accesql",
		Short: "Herramienta para leer bases Microsoft Access (solo lectura)",
		Long: `Accesql lee bases de Microsoft Access (.accdb / .mdb) en modo solo lectura y las
ofrece como JSON, como servidor MySQL o como scripts SQL de MySQL.

Sin argumentos abre un menú interactivo en la terminal (es lo que ocurre con
doble clic): abrir una base, generar la imagen MySQL, exponerla como servidor,
consultarla, convertirla y ver sus relaciones. También puede abrir la interfaz
gráfica en el navegador, sobre la misma base ya cargada.`,
		Example: `  accesql                      # menú interactivo
  accesql -f datos.accdb       # menú con la base ya elegida
  accesql dump -f datos.accdb  # JSON, para scripts y automatización`,
		SilenceUsage: true, // no imprimir el uso completo ante cada error
		// Sin argumentos (por ejemplo, con doble clic) abre el menú interactivo.
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTerminal() {
				return cmd.Help()
			}
			sess := session.New()
			defer sess.Close()
			if file != "" {
				if err := sess.Open(file); err != nil {
					return err
				}
			}
			a := app.New(sess)
			defer a.Close()
			// WithAltScreen usa la pantalla alternativa: al salir, la terminal
			// queda como estaba antes.
			_, err := tea.NewProgram(a, tea.WithAltScreen()).Run()
			return err
		},
	}
	root.Flags().StringVarP(&file, "file", "f", "", "archivo .accdb/.mdb a preseleccionar en el menú")
	root.AddCommand(newDumpCmd())
	root.AddCommand(newQueryCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newWebCmd())
	return root
}

// Execute corre el CLI. Lo llama main.
func Execute() error {
	return newRootCmd().Execute()
}
