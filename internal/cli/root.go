// Package cli define los comandos de línea de comandos (cobra).
package cli

import (
	"os"

	"access2mysql/internal/tui/app"

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
	root := &cobra.Command{
		Use:          "access-tool",
		Short:        "Herramienta para leer bases Microsoft Access (solo lectura)",
		SilenceUsage: true, // no imprimir el uso completo ante cada error
		// Sin argumentos (por ejemplo, con doble clic) abre el menú interactivo.
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTerminal() {
				return cmd.Help()
			}
			_, err := tea.NewProgram(app.New(), tea.WithAltScreen()).Run()
			return err
		},
	}
	root.AddCommand(newDumpCmd())
	root.AddCommand(newQueryCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newUICmd())
	root.AddCommand(newWebCmd())
	return root
}

// Execute corre el CLI. Lo llama main.
func Execute() error {
	return newRootCmd().Execute()
}
