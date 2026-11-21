package cli

import (
	"fmt"

	"accesql/internal/access"
	"accesql/internal/engine"

	"github.com/spf13/cobra"
)

func newQueryCmd() *cobra.Command {
	var (
		file string
		sql  string
	)

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Ejecuta una consulta SQL (estilo MySQL) y devuelve JSON",
		Long: `Carga el archivo Access en memoria y ejecuta una consulta con dialecto MySQL
(JOIN, GROUP BY, subconsultas, etc.). Los nombres con espacios o símbolos van
entre comillas invertidas: SELECT * FROM ` + "`Order Details`" + `.

Los datos son una foto de solo lectura: nada se escribe en el .accdb.`,
		Example: `  accesql query -f testdb/Northwind.accdb -q "SELECT ID, Company FROM Customers LIMIT 3"`,
		PreRunE: requireFlags("file", "sql"),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := access.Open(file)
			if err != nil {
				return err
			}
			defer src.Close()

			eng, err := engine.New(src, engine.DefaultDatabase)
			if err != nil {
				return err
			}
			res, err := eng.Query(sql)
			if err != nil {
				return fmt.Errorf("consulta: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), res)
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "archivo .accdb/.mdb (obligatorio)")
	cmd.Flags().StringVarP(&sql, "sql", "q", "", "consulta SQL (obligatorio)")
	return cmd
}
