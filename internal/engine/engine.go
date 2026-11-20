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

	"access2mysql/internal/access"

	sqle "github.com/dolthub/go-mysql-server"
	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
)

// dbName es el nombre de la base dentro del motor (lo que MySQL llamaría
// `USE access`).
const dbName = "access"

// Engine es una base Access cargada y lista para consultar.
type Engine struct {
	eng *sqle.Engine
	ctx *sql.Context
}

// New copia todas las tablas de src al motor.
func New(src *access.DB) (*Engine, error) {
	tables, err := src.Tables()
	if err != nil {
		return nil, err
	}

	db := memory.NewDatabase(dbName)
	provider := memory.NewDBProvider(db)
	session := memory.NewSession(sql.NewBaseSession(), provider)
	ctx := sql.NewContext(context.Background(), sql.WithSession(session))
	ctx.SetCurrentDatabase(dbName)

	for _, name := range tables {
		t, err := src.Load(name)
		if err != nil {
			return nil, err
		}
		if err := loadTable(ctx, db, t); err != nil {
			return nil, fmt.Errorf("cargando tabla %q: %w", name, err)
		}
	}
	return &Engine{eng: sqle.NewDefault(provider), ctx: ctx}, nil
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
	schema, iter, _, err := e.eng.Query(e.ctx, q)
	if err != nil {
		return nil, err
	}
	defer iter.Close(e.ctx)

	res := &Result{Columns: make([]string, len(schema)), Rows: [][]any{}}
	for i, c := range schema {
		res.Columns[i] = c.Name
	}
	for {
		row, err := iter.Next(e.ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		out := make([]any, len(row))
		for i, v := range row {
			out[i] = jsonValue(v)
		}
		res.Rows = append(res.Rows, out)
	}
	return res, nil
}
