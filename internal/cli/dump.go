package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"accesql/internal/access"
	"accesql/internal/jsonout"

	"github.com/spf13/cobra"
)

func newDumpCmd() *cobra.Command {
	var (
		file   string
		outDir string
		table  string
	)

	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Exporta tablas de un Access a JSON",
		Example: `  accesql dump --file testdb/Northwind.accdb --table Products
  accesql dump --file testdb/Northwind.accdb --out salida/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := access.Open(file)
			if err != nil {
				return err
			}
			defer db.Close()

			tables, err := db.Tables()
			if err != nil {
				return err
			}
			if table != "" {
				tables = []string{table}
			}

			// Sin --out: un único objeto JSON {"tabla": [filas...]} por stdout,
			// con las tablas en el orden del archivo.
			if outDir == "" {
				all := make(jsonout.Object, 0, len(tables))
				for _, t := range tables {
					rows, err := db.ReadTable(t)
					if err != nil {
						return err
					}
					all = append(all, jsonout.Field{Key: t, Value: rows})
				}
				return writeJSON(cmd.OutOrStdout(), all)
			}

			// Con --out: un archivo <tabla>.json por tabla.
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}
			for _, t := range tables {
				rows, err := db.ReadTable(t)
				if err != nil {
					return err
				}
				path := filepath.Join(outDir, t+".json")
				if err := writeJSONFile(path, rows); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %d filas\n", path, len(rows.Rows))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "archivo .accdb/.mdb (obligatorio)")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "carpeta de salida (un .json por tabla); sin esto imprime por stdout")
	cmd.Flags().StringVarP(&table, "table", "t", "", "exportar solo esta tabla")
	cmd.MarkFlagRequired("file")
	return cmd
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeJSONFile(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeJSON(f, v)
}
