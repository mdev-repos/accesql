// Package engine carga una base Access en un motor SQL en memoria con dialecto
// MySQL (go-mysql-server), para poder consultarla con JOINs, agregaciones, etc.
//
// Es una FOTO: los datos se copian al arrancar. Si el .accdb cambia después,
// hay que volver a cargar. Las escrituras SQL afectan solo la copia en
// memoria, nunca el archivo.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"accesql/internal/access"

	sqle "github.com/dolthub/go-mysql-server"
	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
)

// DefaultDatabase es el nombre por defecto de la base dentro del motor (lo que
// MySQL llamaría `USE access`).
const DefaultDatabase = "access"

// Engine es una base Access cargada y lista para consultar.
type Engine struct {
	eng      *sqle.Engine
	ctx      *sql.Context
	provider *memory.DbProvider
	db       *memory.Database
	dbName   string

	// meta guarda las columnas originales de Access de cada tabla, para
	// mostrar sus tipos en las interfaces (el motor SQL solo conoce los tipos
	// traducidos). loadedAt es el momento de la última carga completa.
	meta      map[string][]access.Column
	relations []access.Relation // relaciones definidas en Access (pueden ser ninguna)
	loadedAt  time.Time

	// sandbox es el directorio aislado de Lockdown ("" si no se aplicó).
	lockMu  sync.Mutex
	sandbox string

	// internalReady indica que la sesión interna ya tiene su cuenta (ver
	// InternalUser). Se protege con mu.
	internalReady bool

	// mu coordina las consultas con el reload: cada consulta toma RLock (hay
	// muchas a la vez) y Reload toma Lock, que espera a las que están en curso
	// y frena las nuevas hasta terminar.
	mu sync.RWMutex
}

// New copia todas las tablas de src al motor, dentro de una base llamada
// dbName (las apps que se conecten por red la usarán como nombre de base).
func New(src *access.DB, dbName string) (*Engine, error) {
	return NewWithProgress(context.Background(), src, dbName, nil)
}

// NewWithProgress es como New pero informa el avance y se puede cancelar:
// antes de leer cada tabla llama a onProgress(nombre, número, total) (el número
// empieza en 1) y revisa si ctx fue cancelado. onProgress puede ser nil.
func NewWithProgress(ctx context.Context, src *access.DB, dbName string, onProgress func(table string, n, total int)) (*Engine, error) {
	tables, err := src.Tables()
	if err != nil {
		return nil, err
	}

	db := memory.NewDatabase(dbName)
	provider := memory.NewDBProvider(db)
	session := memory.NewSession(sql.NewBaseSession(), provider)
	sqlCtx := sql.NewContext(context.Background(), sql.WithSession(session))
	sqlCtx.SetCurrentDatabase(dbName)

	meta := make(map[string][]access.Column, len(tables))
	for i, name := range tables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if onProgress != nil {
			onProgress(name, i+1, len(tables))
		}
		t, err := src.Load(name)
		if err != nil {
			return nil, err
		}
		if err := loadTable(sqlCtx, db, t); err != nil {
			return nil, fmt.Errorf("cargando tabla %q: %w", name, err)
		}
		meta[name] = t.Columns
	}

	// Las relaciones son un extra: si no se pueden leer, la base igual sirve.
	rels, _ := src.Relations()

	return &Engine{
		eng: sqle.NewDefault(provider), ctx: sqlCtx, provider: provider, db: db, dbName: dbName,
		meta: meta, relations: rels, loadedAt: time.Now(),
	}, nil
}

// loadTable crea la tabla en memoria con el esquema de Access y la llena.
// No se crean claves primarias ni índices: Access no los expone por esta vía.
func loadTable(ctx *sql.Context, db *memory.Database, t *access.Table) error {
	schema := make(sql.Schema, len(t.Columns))
	for i, c := range t.Columns {
		schema[i] = &sql.Column{
			Name:     c.Name,
			Type:     sqlType(t, i),
			Source:   t.Name,
			Nullable: true,
		}
	}
	table := memory.NewTable(db, t.Name, sql.NewPrimaryKeySchema(schema), db.GetForeignKeyCollection())
	db.AddTable(t.Name, table)

	// Un solo Inserter para toda la tabla (table.Insert abriría uno por fila).
	ins := table.Inserter(ctx)
	for n, values := range t.Rows {
		row := make(sql.Row, len(values))
		for i, v := range values {
			cv, err := toSQLValue(ctx, schema[i].Type, t.Columns[i], v)
			if err != nil {
				return fmt.Errorf("fila %d: %w", n+1, err)
			}
			row[i] = cv
		}
		if err := ins.Insert(ctx, row); err != nil {
			return fmt.Errorf("fila %d: %w", n+1, err)
		}
	}
	return ins.Close(ctx)
}

// Query ejecuta una sentencia SQL (dialecto MySQL) y devuelve el resultado.
func (e *Engine) Query(q string) (*Result, error) {
	res, _, err := e.QueryN(q, 0)
	return res, err
}

// QueryN es como Query pero devuelve como máximo max filas (max <= 0 significa
// sin límite). truncated indica que había más filas que las devueltas. Sirve
// para interfaces interactivas, donde un SELECT * sobre una tabla enorme no
// debe llenar la memoria ni colgar la pantalla.
//
// No es seguro llamarla desde varias goroutines a la vez: todas comparten una
// sesión del motor.
func (e *Engine) QueryN(q string, max int) (*Result, bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.queryN(q, max)
}

// queryN es QueryN sin tomar el candado: para usar desde métodos que ya lo
// tienen (un RLock repetido en la misma goroutine puede trabarse si un Reload
// está esperando entre medio).
func (e *Engine) queryN(q string, max int) (res *Result, truncated bool, err error) {
	schema, iter, _, err := e.eng.Query(e.ctx, q)
	if err != nil {
		return nil, false, err
	}
	defer iter.Close(e.ctx)

	res = &Result{Columns: make([]string, len(schema)), Rows: [][]any{}}
	for i, c := range schema {
		res.Columns[i] = c.Name
	}
	for {
		row, err := iter.Next(e.ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		if max > 0 && len(res.Rows) >= max {
			return res, true, nil
		}
		out := make([]any, len(row))
		for i, v := range row {
			out[i] = jsonValue(v)
		}
		res.Rows = append(res.Rows, out)
	}
	return res, false, nil
}

// ColumnInfo describe una columna para las interfaces.
type ColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`           // tipo SQL (INT, DECIMAL(19,4), LONGTEXT...)
	AccessType string `json:"accessType"`     // tipo original en Access ("Long Integer", "Text"...)
	Size       int    `json:"size,omitempty"` // largo declarado en Access (Text(100) → 100)
}

// TableInfo describe una tabla: sus columnas y cuántas filas tiene.
type TableInfo struct {
	Name    string       `json:"name"`
	Rows    int64        `json:"rows"`
	Columns []ColumnInfo `json:"columns"`
}

// TableInfos devuelve el detalle de todas las tablas, ordenado por nombre.
func (e *Engine) TableInfos() ([]TableInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	names, err := e.db.GetTableNames(e.ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	infos := make([]TableInfo, 0, len(names))
	for _, name := range names {
		tbl, ok, err := e.db.GetTableInsensitive(e.ctx, name)
		if err != nil || !ok {
			return nil, fmt.Errorf("tabla %q: %v", name, err)
		}
		info := TableInfo{Name: name}
		orig := e.meta[name]
		for i, c := range tbl.Schema() {
			ci := ColumnInfo{Name: c.Name, Type: c.Type.String()}
			if i < len(orig) {
				ci.AccessType = orig[i].Type
				ci.Size = orig[i].Size
			}
			info.Columns = append(info.Columns, ci)
		}
		res, _, err := e.queryN("SELECT COUNT(*) FROM `"+name+"`", 0)
		if err != nil {
			return nil, err
		}
		if len(res.Rows) == 1 {
			if n, ok := res.Rows[0][0].(int64); ok {
				info.Rows = n
			}
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Relations devuelve las relaciones entre tablas definidas en Access.
func (e *Engine) Relations() []access.Relation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]access.Relation(nil), e.relations...)
}

// Scan recorre todas las filas de una tabla llamando a fn por cada una, SIN
// acumularlas: sirve para generar archivos grandes (scripts SQL, volcados) con
// memoria constante. Los valores salen ya adaptados (fechas sin zona, Numeric
// como json.Number). Si fn devuelve un error, el recorrido se corta con él.
//
// Mismas reglas que QueryN: no llamarla desde varias goroutines a la vez.
func (e *Engine) Scan(table string, fn func(row []any) error) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	_, iter, _, err := e.eng.Query(e.ctx, "SELECT * FROM "+quoteIdent(table))
	if err != nil {
		return err
	}
	defer iter.Close(e.ctx)
	for {
		row, err := iter.Next(e.ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]any, len(row))
		for i, v := range row {
			out[i] = jsonValue(v)
		}
		if err := fn(out); err != nil {
			return err
		}
	}
}

// quoteIdent escribe un nombre de tabla o columna entre comillas invertidas
// (duplicando las que contenga), como espera MySQL.
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}
