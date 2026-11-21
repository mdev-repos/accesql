package access

import (
	"fmt"
	"sort"
	"strings"
)

// Relation es una relación entre dos tablas definida en Access (la ventana
// "Relaciones"). Table es la tabla "hija", la que guarda la clave foránea, y
// RefTable es la "padre", a la que apunta.
//
// Una relación puede abarcar varias columnas (clave compuesta): Columns[i]
// se corresponde con RefColumns[i].
type Relation struct {
	Name          string   `json:"name"`
	Table         string   `json:"table"`
	Columns       []string `json:"columns"`
	RefTable      string   `json:"refTable"`
	RefColumns    []string `json:"refColumns"`
	Enforced      bool     `json:"enforced"` // Access exige integridad referencial
	CascadeUpdate bool     `json:"cascadeUpdate"`
	CascadeDelete bool     `json:"cascadeDelete"`
	OneToOne      bool     `json:"oneToOne"`
}

// Banderas de la columna grbit de MSysRelationships (documentadas por
// los proyectos Jackcess y mdbtools).
const (
	relOneToOne      = 0x00000001
	relNoIntegrity   = 0x00000002
	relCascadeUpdate = 0x00000100
	relCascadeDelete = 0x00001000
)

// Relations lee las relaciones definidas por el usuario desde la tabla interna
// MSysRelationships. Ignora las del sistema (MSys*).
func (d *DB) Relations() ([]Relation, error) {
	rows, err := d.sqlDB.Query("SELECT * FROM [MSysRelationships]")
	if err != nil {
		return nil, fmt.Errorf("leyendo relaciones: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		idx[strings.ToLower(c)] = i
	}
	for _, need := range []string{"szrelationship", "grbit", "icolumn", "szobject", "szcolumn", "szreferencedobject", "szreferencedcolumn"} {
		if _, ok := idx[need]; !ok {
			return nil, fmt.Errorf("leyendo relaciones: falta la columna %q", need)
		}
	}

	type part struct {
		pos         int
		col, refCol string
	}
	type rel struct {
		Relation
		parts []part
	}
	byName := map[string]*rel{}
	var order []string

	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("leyendo relaciones: %w", err)
		}
		str := func(name string) string { s, _ := vals[idx[name]].(string); return s }
		num := func(name string) int64 {
			switch n := vals[idx[name]].(type) {
			case int64:
				return n
			case int32:
				return int64(n)
			case int:
				return int64(n)
			case int16:
				return int64(n)
			}
			return 0
		}

		name, table, ref := str("szrelationship"), str("szobject"), str("szreferencedobject")
		if strings.HasPrefix(table, "MSys") || strings.HasPrefix(ref, "MSys") {
			continue
		}
		r, ok := byName[name]
		if !ok {
			g := num("grbit")
			r = &rel{Relation: Relation{
				Name: name, Table: table, RefTable: ref,
				Enforced:      g&relNoIntegrity == 0,
				CascadeUpdate: g&relCascadeUpdate != 0,
				CascadeDelete: g&relCascadeDelete != 0,
				OneToOne:      g&relOneToOne != 0,
			}}
			byName[name] = r
			order = append(order, name)
		}
		r.parts = append(r.parts, part{int(num("icolumn")), str("szcolumn"), str("szreferencedcolumn")})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("leyendo relaciones: %w", err)
	}

	out := make([]Relation, 0, len(order))
	for _, name := range order {
		r := byName[name]
		sort.Slice(r.parts, func(i, j int) bool { return r.parts[i].pos < r.parts[j].pos })
		for _, p := range r.parts {
			r.Columns = append(r.Columns, p.col)
			r.RefColumns = append(r.RefColumns, p.refCol)
		}
		out = append(out, r.Relation)
	}
	return out, nil
}
