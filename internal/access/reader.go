// Package access lee bases de datos Microsoft Access (.mdb/.accdb) en modo
// solo lectura. Envuelve el driver database/sql de mdbgo para que el resto de
// la aplicación no dependa de esa librería.
package access

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	// El guion bajo importa el paquete solo por su efecto secundario:
	// registrar el driver "gomdb" (Go puro, sin CGO) en database/sql.
	_ "github.com/Felamande/mdbgo/driver/gomdb"
)

// DB representa una base Access abierta.
type DB struct {
	sqlDB *sql.DB
}

// Row es una fila: nombre de columna -> valor.
type Row map[string]any

// Open abre el archivo Access indicado.
func Open(path string) (*DB, error) {
	db, err := sql.Open("gomdb", path)
	if err != nil {
		return nil, fmt.Errorf("abriendo %q: %w", path, err)
	}
	// sql.Open no toca el archivo; Ping fuerza a que se abra de verdad.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("leyendo %q: %w", path, err)
	}
	return &DB{sqlDB: db}, nil
}

// Close libera el archivo.
func (d *DB) Close() error { return d.sqlDB.Close() }

// Tables devuelve los nombres de las tablas de usuario.
func (d *DB) Tables() ([]string, error) {
	rows, err := d.sqlDB.Query("LIST TABLES")
	if err != nil {
		return nil, fmt.Errorf("listando tablas: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("leyendo nombre de tabla: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// ReadTable devuelve todas las filas de una tabla.
func (d *DB) ReadTable(table string) ([]Row, error) {
	rows, err := d.sqlDB.Query("SELECT * FROM [" + table + "]")
	if err != nil {
		return nil, fmt.Errorf("consultando tabla %q: %w", table, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	// Tipos según Access ("Numeric", "Text", ...). Los consultamos una vez por
	// tabla, no por fila. Las columnas Numeric llegan como string del driver;
	// las marcamos para emitirlas como número JSON.
	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	isNumeric := make([]bool, len(cols))
	for i, ct := range colTypes {
		isNumeric[i] = ct.DatabaseTypeName() == "Numeric"
	}

	result := []Row{} // slice vacío (no nil) para que JSON salga [] y no null
	for rows.Next() {
		// Scan necesita punteros: armamos un slice de valores y otro de
		// punteros a cada posición.
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("tabla %q: %w", table, err)
		}
		row := make(Row, len(cols))
		for i, c := range cols {
			row[c] = normalize(values[i])
			// json.Number se escribe sin comillas y conserva los decimales
			// exactos (no pasa por float64).
			if s, ok := values[i].(string); ok && isNumeric[i] {
				row[c] = json.Number(s)
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// normalize ajusta valores para que el JSON sea fiel a lo guardado en Access.
//
// Access guarda fechas SIN zona horaria. El driver las devuelve como
// time.Time en la zona local de la máquina, y si las serializáramos tal cual
// saldría un offset inventado (que además cambiaría según dónde se ejecute).
// Las emitimos como fecha "ingenua": 2006-01-15T00:00:00.
func normalize(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.Format("2006-01-02T15:04:05")
	}
	return v
}
