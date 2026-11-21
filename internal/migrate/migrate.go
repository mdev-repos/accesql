package migrate

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"accesql/internal/access"
	"accesql/internal/engine"
	"accesql/internal/export"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Source es lo que migrate necesita de la base cargada. engine.Engine lo cumple;
// es una interfaz para poder probar con datos falsos.
type Source interface {
	TableInfos() ([]engine.TableInfo, error)
	Relations() []access.Relation
	Scan(table string, fn func(row []any) error) error
}

// Stats resume lo que se generó. Notes son avisos para mostrar al usuario
// (una relación omitida, una columna que pasó a TEXT, etc.).
type Stats struct {
	Tables int
	Rows   int64
	Notes  []string
}

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_$]{1,64}$`)

// ValidDatabaseName dice si es un nombre de base válido para el script.
func ValidDatabaseName(s string) bool { return dbNameRe.MatchString(s) }

// ---------------------------------------------------------------------------
// Plan: tipos, claves y relaciones de cada tabla
// ---------------------------------------------------------------------------

type fkPlan struct {
	name             string
	cols             []string
	refTable         string
	refCols          []string
	onDelete, onUpda bool
}

type tablePlan struct {
	info   engine.TableInfo
	types  []string
	pk     []string
	pkNote string
	keys   [][]string // índices extra: columnas que otras tablas referencian
	fks    []fkPlan
}

func (t *tablePlan) colIndex(name string) int {
	for i, c := range t.info.Columns {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

// resolve devuelve los nombres exactos de las columnas, o nil si alguna no existe.
func (t *tablePlan) resolve(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		j := t.colIndex(n)
		if j < 0 {
			return nil
		}
		out[i] = t.info.Columns[j].Name
	}
	return out
}

func indexable(typ string) bool {
	switch {
	case strings.HasPrefix(typ, "LONG"), typ == "TEXT", typ == "FLOAT", typ == "DOUBLE":
		return false
	}
	return true
}

type plan struct {
	tables []*tablePlan
	byName map[string]*tablePlan
	notes  []string
}

func buildPlan(ctx context.Context, src Source, infos []engine.TableInfo, foreignKeys bool) (*plan, error) {
	p := &plan{byName: map[string]*tablePlan{}}
	for _, info := range infos {
		tp := &tablePlan{info: info, types: make([]string, len(info.Columns))}
		for i, c := range info.Columns {
			tp.types[i] = mysqlType(c)
		}
		if fitRowSize(tp.types) {
			p.notes = append(p.notes, fmt.Sprintf("Tabla %q: tiene tantas columnas de texto que no entra en una fila de MySQL; las más anchas se crearon como TEXT.", info.Name))
		}
		p.tables = append(p.tables, tp)
		p.byName[strings.ToLower(info.Name)] = tp
	}
	rels := src.Relations()

	// Claves primarias.
	for _, tp := range p.tables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := inferKey(ctx, src, tp, rels); err != nil {
			return nil, err
		}
	}

	// Claves foráneas (solo las que Access exigía) e índices que necesitan.
	used := map[string]bool{}
	for _, r := range rels {
		child, parent := p.byName[strings.ToLower(r.Table)], p.byName[strings.ToLower(r.RefTable)]
		if child == nil || parent == nil {
			continue
		}
		if !foreignKeys {
			continue
		}
		if !r.Enforced {
			p.notes = append(p.notes, fmt.Sprintf("Relación %q (%s → %s): Access no exigía integridad referencial, no se creó FOREIGN KEY.", r.Name, child.info.Name, parent.info.Name))
			continue
		}
		cols, refCols := child.resolve(r.Columns), parent.resolve(r.RefColumns)
		if cols == nil || refCols == nil || len(cols) != len(refCols) {
			p.notes = append(p.notes, fmt.Sprintf("Relación %q: columnas no encontradas, se omitió.", r.Name))
			continue
		}
		ok := true
		for i := range cols {
			ct, pt := child.types[child.colIndex(cols[i])], parent.types[parent.colIndex(refCols[i])]
			if ct != pt || !indexable(ct) {
				ok = false
			}
		}
		if !ok {
			p.notes = append(p.notes, fmt.Sprintf("Relación %q (%s → %s): las columnas tienen tipos distintos, no se creó FOREIGN KEY.", r.Name, child.info.Name, parent.info.Name))
			continue
		}
		if !hasPrefix(parent.pk, refCols) && !containsKey(parent.keys, refCols) {
			parent.keys = append(parent.keys, refCols)
		}
		child.fks = append(child.fks, fkPlan{
			name: fkName(r.Name, used), cols: cols, refTable: parent.info.Name, refCols: refCols,
			onDelete: r.CascadeDelete, onUpda: r.CascadeUpdate,
		})
	}
	return p, nil
}

func hasPrefix(have, want []string) bool {
	if len(have) < len(want) {
		return false
	}
	for i := range want {
		if !strings.EqualFold(have[i], want[i]) {
			return false
		}
	}
	return true
}

func containsKey(keys [][]string, want []string) bool {
	for _, k := range keys {
		if hasPrefix(k, want) {
			return true
		}
	}
	return false
}

// inferKey busca la clave primaria de la tabla. Access no la expone por esta
// vía, así que se deduce de dos pistas: las columnas que otra tabla referencia
// en una relación, o una columna llamada ID / "<tabla> ID". Una pista solo se
// acepta si los DATOS la confirman: sin vacíos ni repetidos (comparando como
// MySQL, sin distinguir mayúsculas ni acentos). Si no, no se declara clave.
func inferKey(ctx context.Context, src Source, tp *tablePlan, rels []access.Relation) error {
	type cand struct {
		cols []string
		why  string
	}
	var cands []cand
	for _, r := range rels {
		if strings.EqualFold(r.RefTable, tp.info.Name) {
			if cols := tp.resolve(r.RefColumns); cols != nil {
				cands = append(cands, cand{cols, "columnas referenciadas por la relación " + r.Name})
			}
		}
	}
	tl := strings.ToLower(tp.info.Name)
	for _, c := range tp.info.Columns {
		switch l := strings.ToLower(c.Name); l {
		case "id", tl + " id", tl + "id", tl + "_id", "id " + tl, "id_" + tl:
			cands = append(cands, cand{[]string{c.Name}, "columna " + c.Name})
		}
	}
	for _, c := range cands {
		ok := true
		for _, name := range c.cols {
			if !indexable(tp.types[tp.colIndex(name)]) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		unique, err := uniqueNonNull(ctx, src, tp, c.cols)
		if err != nil {
			return err
		}
		if unique {
			tp.pk, tp.pkNote = c.cols, "clave primaria deducida de: "+c.why
			return nil
		}
	}
	return nil
}

var foldAccents = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// foldKey normaliza un valor como lo haría la collation de MySQL por defecto:
// sin mayúsculas ni acentos, y sin espacios al final.
func foldKey(v any) string {
	s := fmt.Sprint(v)
	if f, _, err := transform.String(foldAccents, s); err == nil {
		s = f
	}
	return strings.TrimRight(strings.ToLower(s), " ")
}

func uniqueNonNull(ctx context.Context, src Source, tp *tablePlan, cols []string) (bool, error) {
	idx := make([]int, len(cols))
	for i, c := range cols {
		idx[i] = tp.colIndex(c)
	}
	seen := map[string]struct{}{}
	n := 0
	err := src.Scan(tp.info.Name, func(row []any) error {
		if n++; n%2000 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		parts := make([]string, len(idx))
		for i, j := range idx {
			if row[j] == nil {
				return errNotKey
			}
			parts[i] = foldKey(row[j])
		}
		k := strings.Join(parts, "\x00")
		if _, dup := seen[k]; dup {
			return errNotKey
		}
		seen[k] = struct{}{}
		return nil
	})
	if err == errNotKey {
		return false, nil
	}
	return err == nil, err
}

var errNotKey = fmt.Errorf("no es clave")

// PrimaryKeys devuelve la clave primaria deducida de cada tabla (por nombre de
// tabla; las que no tienen una clave confiable no aparecen). Es la misma
// deducción que usan los scripts, para que el diagrama y el SQL coincidan.
func PrimaryKeys(ctx context.Context, src Source) (map[string][]string, error) {
	infos, err := src.TableInfos()
	if err != nil {
		return nil, err
	}
	pl, err := buildPlan(ctx, src, infos, false)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, tp := range pl.tables {
		if len(tp.pk) > 0 {
			out[tp.info.Name] = tp.pk
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Crear una base nueva
// ---------------------------------------------------------------------------

// NewOptions configura WriteNew.
type NewOptions struct {
	Source       string // archivo de Access de origen (solo para el encabezado)
	Version      string // versión de Accesql (solo para el encabezado)
	Database     string // nombre de la base a crear
	Data         bool   // incluir los INSERT con los datos
	DropDatabase bool   // agregar DROP DATABASE IF EXISTS
	ForeignKeys  bool   // crear las relaciones como FOREIGN KEY
}

// WriteNew escribe un script que crea una base de datos MySQL con todas las
// tablas (y, si se pide, sus datos y relaciones).
func WriteNew(ctx context.Context, w io.Writer, src Source, o NewOptions) (Stats, error) {
	if !ValidDatabaseName(o.Database) {
		return Stats{}, fmt.Errorf("nombre de base inválido %q: usá letras, números, _ o $ (hasta 64 caracteres)", o.Database)
	}
	infos, err := src.TableInfos()
	if err != nil {
		return Stats{}, err
	}
	pl, err := buildPlan(ctx, src, infos, o.ForeignKeys)
	if err != nil {
		return Stats{}, err
	}

	st := Stats{Tables: len(pl.tables), Notes: pl.notes}
	for _, t := range infos {
		st.Rows += t.Rows
	}

	bw := bufio.NewWriterSize(w, 64<<10)
	pf := func(format string, a ...any) { fmt.Fprintf(bw, format, a...) }

	pf("-- Accesql %s · %s → MySQL\n", o.Version, o.Source)
	pf("-- Tablas: %d · Filas: %d\n", st.Tables, st.Rows)
	for _, n := range pl.notes {
		pf("-- AVISO: %s\n", n)
	}
	pf("\nSET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS = 0;\n\n")
	if o.DropDatabase {
		pf("DROP DATABASE IF EXISTS %s;\n", export.QuoteIdent(o.Database))
	}
	pf("CREATE DATABASE %s DEFAULT CHARACTER SET utf8mb4;\nUSE %s;\n\n", export.QuoteIdent(o.Database), export.QuoteIdent(o.Database))

	for _, tp := range pl.tables {
		writeCreate(bw, tp)
	}

	if o.Data {
		for _, tp := range pl.tables {
			if tp.info.Rows == 0 {
				continue
			}
			pf("-- Datos de %s (%d filas)\n", tp.info.Name, tp.info.Rows)
			cols := make([]string, len(tp.info.Columns))
			for i, c := range tp.info.Columns {
				cols[i] = c.Name
			}
			iw := export.NewInsertWriter(bw, tp.info.Name, cols)
			n := 0
			err := src.Scan(tp.info.Name, func(row []any) error {
				if n++; n%1000 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				return iw.Add(row)
			})
			if err != nil {
				return st, err
			}
			if err := iw.Flush(); err != nil {
				return st, err
			}
			pf("\n")
		}
	}
	pf("SET FOREIGN_KEY_CHECKS = 1;\n")
	return st, bw.Flush()
}

func writeCreate(bw *bufio.Writer, tp *tablePlan) {
	pf := func(format string, a ...any) { fmt.Fprintf(bw, format, a...) }
	pf("-- Tabla %s (%d filas)\n", tp.info.Name, tp.info.Rows)
	if tp.pkNote != "" {
		pf("-- %s\n", tp.pkNote)
	}
	pf("CREATE TABLE %s (\n", export.QuoteIdent(tp.info.Name))

	var lines []string
	pkSet := map[string]bool{}
	for _, c := range tp.pk {
		pkSet[strings.ToLower(c)] = true
	}
	for i, c := range tp.info.Columns {
		def := "  " + export.QuoteIdent(c.Name) + " " + tp.types[i]
		if pkSet[strings.ToLower(c.Name)] {
			def += " NOT NULL"
		}
		lines = append(lines, def)
	}
	quoteList := func(cols []string) string {
		q := make([]string, len(cols))
		for i, c := range cols {
			q[i] = export.QuoteIdent(c)
		}
		return strings.Join(q, ", ")
	}
	if len(tp.pk) > 0 {
		lines = append(lines, "  PRIMARY KEY ("+quoteList(tp.pk)+")")
	}
	for i, k := range tp.keys {
		lines = append(lines, fmt.Sprintf("  KEY %s (%s)", export.QuoteIdent(fmt.Sprintf("ix_ref_%d", i+1)), quoteList(k)))
	}
	for _, fk := range tp.fks {
		s := fmt.Sprintf("  CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
			export.QuoteIdent(fk.name), quoteList(fk.cols), export.QuoteIdent(fk.refTable), quoteList(fk.refCols))
		if fk.onDelete {
			s += " ON DELETE CASCADE"
		}
		if fk.onUpda {
			s += " ON UPDATE CASCADE"
		}
		lines = append(lines, s)
	}
	pf("%s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;\n\n", strings.Join(lines, ",\n"))
}

// ---------------------------------------------------------------------------
// Agregar datos a tablas que ya existen
// ---------------------------------------------------------------------------

// Duplicate dice qué hacer cuando un registro ya existe en MySQL (misma clave).
type Duplicate int

const (
	DupIgnore  Duplicate = iota // omitirlo: INSERT IGNORE
	DupUpdate                   // actualizarlo con los datos de Access
	DupReplace                  // reemplazarlo por completo: REPLACE
)

// TablePair indica de qué tabla de Access a qué tabla de MySQL se copia.
type TablePair struct {
	Source string // tabla de Access
	Target string // tabla de MySQL (puede llamarse distinto)
	// TargetColumns, si no está vacío, es la lista de columnas que tiene la
	// tabla de MySQL. Solo se copian las columnas de Access con ese nombre (sin
	// distinguir mayúsculas); las demás se omiten, porque un INSERT con una
	// columna que no existe falla. Vacío = copiar todas las columnas.
	TargetColumns []string
}

// AddOptions configura WriteAdd.
type AddOptions struct {
	Source      string // archivo de Access de origen (solo para el encabezado)
	Version     string
	Tables      []TablePair
	OnDuplicate Duplicate
	Transaction bool // envolver todo en START TRANSACTION ... COMMIT
}

// WriteAdd escribe un script con solo los INSERT, para sumar los datos de
// Access a tablas de una base MySQL que ya existe.
func WriteAdd(ctx context.Context, w io.Writer, src Source, o AddOptions) (Stats, error) {
	if len(o.Tables) == 0 {
		return Stats{}, fmt.Errorf("no hay tablas para migrar")
	}
	infos, err := src.TableInfos()
	if err != nil {
		return Stats{}, err
	}
	pl, err := buildPlan(ctx, src, infos, false) // solo hace falta para saber las claves
	if err != nil {
		return Stats{}, err
	}

	type job struct {
		tp     *tablePlan
		target string
		cols   []int // posiciones de las columnas a copiar
		skip   []string
	}
	var jobs []job
	for _, pair := range o.Tables {
		tp := pl.byName[strings.ToLower(pair.Source)]
		if tp == nil {
			return Stats{}, fmt.Errorf("la tabla %q no existe en la base de Access", pair.Source)
		}
		target := strings.TrimSpace(pair.Target)
		if target == "" {
			target = tp.info.Name
		}
		j := job{tp: tp, target: target}
		want := map[string]bool{}
		for _, c := range pair.TargetColumns {
			if c = strings.TrimSpace(c); c != "" {
				want[strings.ToLower(c)] = true
			}
		}
		for i, c := range tp.info.Columns {
			if len(want) == 0 || want[strings.ToLower(c.Name)] {
				j.cols = append(j.cols, i)
			} else {
				j.skip = append(j.skip, c.Name)
			}
		}
		if len(j.cols) == 0 {
			return Stats{}, fmt.Errorf("ninguna columna de %q coincide con las columnas de %q", tp.info.Name, target)
		}
		jobs = append(jobs, j)
	}

	st := Stats{Tables: len(jobs)}
	bw := bufio.NewWriterSize(w, 64<<10)
	pf := func(format string, a ...any) { fmt.Fprintf(bw, format, a...) }

	pf("-- Accesql %s · %s → tablas existentes de MySQL\n", o.Version, o.Source)
	pf("-- Tablas: %d · Si el registro ya existe: %s\n\n", len(jobs), []string{"se omite", "se actualiza", "se reemplaza"}[o.OnDuplicate])
	pf("SET NAMES utf8mb4;\n")
	if o.Transaction {
		pf("START TRANSACTION;\n")
	}
	pf("\n")

	verb := []string{"INSERT IGNORE INTO", "INSERT INTO", "REPLACE INTO"}[o.OnDuplicate]
	for _, j := range jobs {
		cols := make([]string, len(j.cols))
		for i, k := range j.cols {
			cols[i] = j.tp.info.Columns[k].Name
		}
		pf("-- %s → %s (%d filas)\n", j.tp.info.Name, j.target, j.tp.info.Rows)
		if len(j.skip) > 0 {
			pf("-- Columnas de Access sin equivalente en el destino (no se copian): %s\n", strings.Join(j.skip, ", "))
			st.Notes = append(st.Notes, fmt.Sprintf("%s: no se copian las columnas %s (no están en la tabla de destino).", j.tp.info.Name, strings.Join(j.skip, ", ")))
		}

		tail := ";"
		if o.OnDuplicate == DupUpdate {
			isKey := map[string]bool{}
			for _, k := range j.tp.pk {
				isKey[strings.ToLower(k)] = true
			}
			var sets []string
			for _, c := range cols {
				if !isKey[strings.ToLower(c)] {
					sets = append(sets, fmt.Sprintf("%s = VALUES(%s)", export.QuoteIdent(c), export.QuoteIdent(c)))
				}
			}
			if len(sets) == 0 { // todas son clave: una asignación vacía para que sea válido
				sets = []string{fmt.Sprintf("%s = %s", export.QuoteIdent(cols[0]), export.QuoteIdent(cols[0]))}
			}
			tail = "\nON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ") + ";"
		}

		iw := export.NewCustomInsertWriter(bw, verb, j.target, cols, tail)
		n := 0
		err := src.Scan(j.tp.info.Name, func(row []any) error {
			if n++; n%1000 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			out := make([]any, len(j.cols))
			for i, k := range j.cols {
				out[i] = row[k]
			}
			return iw.Add(out)
		})
		if err != nil {
			return st, err
		}
		if err := iw.Flush(); err != nil {
			return st, err
		}
		st.Rows += int64(n)
		pf("\n")
	}
	if o.Transaction {
		pf("COMMIT;\n")
	}
	return st, bw.Flush()
}
