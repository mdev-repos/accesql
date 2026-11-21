package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"access2mysql/internal/engine"
	"access2mysql/internal/export"
	"access2mysql/internal/migrate"
	"access2mysql/internal/version"
)

type tableInfos = engine.TableInfo

// Format es un formato de exportación de resultados.
type Format string

const (
	FormatJSON     Format = "json"
	FormatCSV      Format = "csv"       // separado por comas
	FormatCSVExcel Format = "csv-excel" // separado por ; y con BOM, para Excel en español
	FormatSQL      Format = "sql"       // sentencias INSERT
)

// ErrTooManyRows indica que el resultado supera el máximo exportable.
var ErrTooManyRows = errors.New("el resultado tiene demasiadas filas para exportar")

// ExportQuery ejecuta la consulta y escribe el resultado en el formato pedido.
// maxRows limita las filas (<= 0 = sin límite): si hay más, devuelve
// ErrTooManyRows en vez de exportar un resultado incompleto sin avisar.
// table es el nombre de tabla de los INSERT (solo para FormatSQL; vacío =
// "resultado").
func (s *Session) ExportQuery(sqlText string, f Format, table string, maxRows int, w io.Writer) error {
	res, truncated, err := s.Query(sqlText, maxRows)
	if err != nil {
		return err
	}
	if truncated {
		return fmt.Errorf("%w (máximo %d)", ErrTooManyRows, maxRows)
	}
	switch f {
	case FormatJSON:
		return export.JSON(w, res)
	case FormatCSV:
		return export.CSV(w, res, false)
	case FormatCSVExcel:
		return export.CSV(w, res, true)
	case FormatSQL:
		if strings.TrimSpace(table) == "" {
			table = "resultado"
		}
		return export.SQLInserts(w, table, res)
	}
	return fmt.Errorf("formato de exportación desconocido: %q", f)
}

// Extension devuelve la extensión de archivo sugerida para un formato.
func (f Format) Extension() string {
	switch f {
	case FormatCSV, FormatCSVExcel:
		return ".csv"
	case FormatSQL:
		return ".sql"
	}
	return ".json"
}

// WriteDump escribe TODAS las tablas como un único JSON {tabla: [filas]}, con
// cada fila como un objeto {columna: valor} y las columnas en el mismo orden
// que en Access: el mismo formato que el comando `dump`. Lee una tabla por vez
// y escribe fila a fila, así que no duplica la base en memoria.
func (s *Session) WriteDump(ctx context.Context, w io.Writer) error {
	e, err := s.engine()
	if err != nil {
		return err
	}
	s.qmu.Lock()
	defer s.qmu.Unlock()

	infos, err := guard(func() ([]tableInfos, error) { return e.TableInfos() })
	if err != nil {
		return err
	}
	put := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}
	if _, err := io.WriteString(w, "{"); err != nil {
		return err
	}
	for i, t := range infos {
		if i > 0 {
			io.WriteString(w, ",")
		}
		io.WriteString(w, "\n  ")
		if err := put(t.Name); err != nil {
			return err
		}
		io.WriteString(w, ": [")
		keys := make([][]byte, len(t.Columns))
		for j, c := range t.Columns {
			if keys[j], err = json.Marshal(c.Name); err != nil {
				return err
			}
		}
		n := 0
		err := e.Scan(t.Name, func(row []any) error {
			if n++; n%1000 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if n > 1 {
				io.WriteString(w, ",")
			}
			io.WriteString(w, "\n    {")
			for j, v := range row {
				if j > 0 {
					io.WriteString(w, ", ")
				}
				w.Write(keys[j])
				io.WriteString(w, ": ")
				if err := put(v); err != nil {
					return err
				}
			}
			_, err := io.WriteString(w, "}")
			return err
		})
		if err != nil {
			return err
		}
		io.WriteString(w, "]")
	}
	_, err = io.WriteString(w, "\n}\n")
	return err
}

// suggestName propone un nombre de base MySQL a partir del archivo abierto
// (Northwind.accdb → northwind). La interfaz lo recibe en State.Suggested.
func suggestName(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		return "mi_base"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// ScriptNew escribe un script MySQL que crea una base nueva con las tablas de
// la imagen (y sus datos y relaciones, si se piden).
func (s *Session) ScriptNew(ctx context.Context, w io.Writer, o migrate.NewOptions) (migrate.Stats, error) {
	e, err := s.engine()
	if err != nil {
		return migrate.Stats{}, err
	}
	o.Source, o.Version = s.sourceName(), version.Version
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return guard(func() (migrate.Stats, error) { return migrate.WriteNew(ctx, w, e, o) })
}

// ScriptAdd escribe un script MySQL con los INSERT para sumar los datos de la
// imagen a tablas que ya existen en MySQL.
func (s *Session) ScriptAdd(ctx context.Context, w io.Writer, o migrate.AddOptions) (migrate.Stats, error) {
	e, err := s.engine()
	if err != nil {
		return migrate.Stats{}, err
	}
	o.Source, o.Version = s.sourceName(), version.Version
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return guard(func() (migrate.Stats, error) { return migrate.WriteAdd(ctx, w, e, o) })
}

func (s *Session) sourceName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return filepath.Base(s.file)
}
