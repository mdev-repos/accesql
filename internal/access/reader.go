// Package access lee bases de datos Microsoft Access (.mdb/.accdb) en modo
// solo lectura. Envuelve el driver database/sql de mdbgo para que el resto de
// la aplicación no dependa de esa librería.
package access

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"accesql/internal/jsonout"

	// El guion bajo importa el paquete solo por su efecto secundario:
	// registrar el driver "gomdb" (Go puro, sin CGO) en database/sql.
	_ "github.com/Felamande/mdbgo/driver/gomdb"
)

// DB representa una base Access abierta.
type DB struct {
	sqlDB *sql.DB
}

// Column describe una columna: su nombre y el tipo según Access
// ("Text", "Long Integer", "Numeric", "DateTime", ...).
type Column struct {
	Name string
	Type string
	Size int // largo declarado en Access (Text(100) → 100); 0 si no aplica o no se conoce
}

// Table es una tabla leída tal cual: esquema y valores crudos, sin adaptar a
// ningún formato de salida. Rows[i][j] corresponde a Columns[j].
type Table struct {
	Name    string
	Columns []Column
	Rows    [][]any
}

// Open abre el archivo Access indicado.
func Open(path string) (*DB, error) {
	// El controlador deja abierto el archivo cuando falla al leerlo (en Windows
	// eso impide borrarlo o moverlo hasta cerrar el programa). Por eso se
	// descartan antes los archivos que claramente no son de Access.
	if err := checkHeader(path); err != nil {
		return nil, err
	}
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

// Load lee una tabla completa: columnas con su tipo y valores crudos.
func (d *DB) Load(table string) (*Table, error) {
	rows, err := d.sqlDB.Query("SELECT * FROM [" + table + "]")
	if err != nil {
		return nil, fmt.Errorf("consultando tabla %q: %w", table, err)
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("tabla %q: %w", table, err)
	}
	t := &Table{Name: table, Columns: make([]Column, len(colTypes)), Rows: [][]any{}}
	for i, ct := range colTypes {
		t.Columns[i] = Column{Name: ct.Name(), Type: ct.DatabaseTypeName()}
		if n, ok := ct.Length(); ok && n > 0 && n < 1<<20 {
			t.Columns[i].Size = int(n)
		}
	}

	for rows.Next() {
		// Scan necesita punteros: armamos un slice de valores y otro de
		// punteros a cada posición.
		values := make([]any, len(t.Columns))
		ptrs := make([]any, len(t.Columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("tabla %q: %w", table, err)
		}
		t.Rows = append(t.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tabla %q: %w", table, err)
	}
	return t, nil
}

// ReadTable devuelve todas las filas de una tabla, adaptadas para JSON y con
// las columnas en el mismo orden que en Access.
func (d *DB) ReadTable(table string) (jsonout.Records, error) {
	t, err := d.Load(table)
	if err != nil {
		return jsonout.Records{}, err
	}

	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = c.Name
	}
	rows := make([][]any, len(t.Rows)) // no nil: JSON sale [] y no null
	for n, values := range t.Rows {
		row := make([]any, len(values))
		for i, v := range values {
			row[i] = forJSON(t.Columns[i], v)
		}
		rows[n] = row
	}
	return jsonout.Records{Columns: cols, Rows: rows}, nil
}

// forJSON ajusta un valor para que el JSON sea fiel a lo guardado en Access.
//
//   - Fechas: Access las guarda SIN zona horaria. El driver las devuelve como
//     time.Time en la zona local de la máquina, y serializarlas tal cual
//     inventaría un offset que cambiaría según dónde se ejecute. Se emiten
//     como fecha "ingenua": 2006-01-15T00:00:00.
//   - Numeric: el driver las entrega como string; json.Number se escribe sin
//     comillas y conserva los decimales exactos (no pasa por float64).
func forJSON(c Column, v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.Format("2006-01-02T15:04:05")
	case string:
		if c.Type == "Numeric" {
			return json.Number(x)
		}
	}
	return v
}

// checkHeader comprueba la firma que llevan todas las bases de Access en el
// byte 4 ("Standard Jet DB" o "Standard ACE DB").
func checkHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("abriendo %q: %w", path, err)
	}
	defer f.Close()
	buf := make([]byte, 19)
	if _, err := io.ReadFull(f, buf); err != nil || !bytes.HasPrefix(buf[4:], []byte("Standard ")) {
		return fmt.Errorf("leyendo %q: no parece una base de Access", path)
	}
	return nil
}
