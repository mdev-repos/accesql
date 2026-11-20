// Package cli define los comandos de línea de comandos (cobra).
package cli

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "access-tool",
		Short:        "Herramienta para leer bases Microsoft Access (solo lectura)",
		SilenceUsage: true, // no imprimir el uso completo ante cada error
	}
	root.AddCommand(newDumpCmd())
	root.AddCommand(newQueryCmd())
	root.AddCommand(newServeCmd())
	return root
}

// Execute corre el CLI. Lo llama main.
func Execute() error {
	return newRootCmd().Execute()
}
