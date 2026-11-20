package cli

import (
	"fmt"
	"path/filepath"

	"access2mysql/internal/access"
	"access2mysql/internal/engine"
	"access2mysql/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

func newUICmd() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Interfaz interactiva en la terminal para explorar tablas y consultar",
		Long: `Abre una interfaz de terminal (TUI): lista de tablas, editor SQL y grilla de
resultados, sin necesidad de escribir comandos. El resultado de cualquier
consulta se puede guardar como JSON con Ctrl+S.

Igual que 'query', trabaja sobre una foto de solo lectura del archivo.`,
		Example: `  access-tool ui -f testdb/Northwind.accdb`,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := access.Open(file)
			if err != nil {
				return err
			}
			defer src.Close()

			fmt.Fprintf(cmd.ErrOrStderr(), "cargando %s ...\n", file)
			eng, err := engine.New(src, engine.DefaultDatabase)
			if err != nil {
				return err
			}
			src.Close()

			model, err := tui.New(eng, filepath.Base(file))
			if err != nil {
				return err
			}
			// WithAltScreen usa la pantalla alternativa: al salir, la terminal
			// queda como estaba antes.
			_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
			return err
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "archivo .accdb/.mdb (obligatorio)")
	cmd.MarkFlagRequired("file")
	return cmd
}
