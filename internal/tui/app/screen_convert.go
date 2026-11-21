package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"accesql/internal/engine"
	"accesql/internal/migrate"
	"accesql/internal/session"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func newFieldInput(val string, w int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 200
	ti.Width = w
	ti.TextStyle = sField
	ti.SetValue(val)
	return ti
}

// previewCmd genera un script en segundo plano y entrega la vista previa. gen
// escribe el script completo en el io.Writer que recibe; el mismo gen se usa
// después para guardarlo entero en un archivo.
func previewCmd(id int, name string, gen func(w io.Writer) error) tea.Cmd {
	return func() tea.Msg {
		text, truncated, err := session.Preview(previewBytes, gen)
		save := func(path string) error {
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			err = gen(f)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			return err
		}
		return scriptReady{id: id, text: text, truncated: truncated, err: err, name: name, save: save}
	}
}

// ---------------------------------------------------------------------------
// Crear una base de datos nueva
// ---------------------------------------------------------------------------

// Filas: 0 nombre · 1 incluir datos · 2 relaciones · 3 borrar si existe ·
// 4 generar · 5 volver.
type convertNew struct {
	id       int
	name     textinput.Model
	withData bool
	fk       bool
	drop     bool
	focus    int
	busy     bool
	err      string
}

const newRows = 6

func newConvertNew(a *App) *convertNew {
	def := a.sess.State().Suggested
	if def == "" {
		def = "mi_base"
	}
	return &convertNew{id: nextID(), name: newFieldInput(def, 28), withData: true, fk: true}
}

// init da el foco al campo del nombre apenas se abre la pantalla.
func (s *convertNew) init() tea.Cmd { return s.name.Focus() }

func (s *convertNew) crumb() string { return "Convertir a MySQL › Base nueva" }
func (s *convertNew) help() string  { return "↑/↓ mover · Enter/Espacio elegir · Esc volver" }

func (s *convertNew) setFocus(i int) tea.Cmd {
	s.focus = (i + newRows) % newRows
	s.name.Blur()
	if s.focus == 0 {
		return s.name.Focus()
	}
	return nil
}

func (s *convertNew) update(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case scriptReady:
		if m.id != s.id {
			return nil
		}
		s.busy = false
		if m.err != nil {
			s.err = oneLine(m.err.Error())
			return nil
		}
		s.err = ""
		return a.push(newScriptScreen(m))
	case tea.KeyMsg:
		return s.key(a, m)
	}
	if s.focus == 0 {
		var cmd tea.Cmd
		s.name, cmd = s.name.Update(msg)
		return cmd
	}
	return nil
}

func (s *convertNew) key(a *App, k tea.KeyMsg) tea.Cmd {
	key := k.String()
	if s.busy {
		return nil
	}
	switch {
	case key == "esc":
		a.pop()
		return nil
	case key == "up" || key == "shift+tab":
		return s.setFocus(s.focus - 1)
	case key == "down" || key == "tab":
		return s.setFocus(s.focus + 1)
	case s.focus == 0 && key == "enter":
		return s.setFocus(1)
	case s.focus == 0:
		var cmd tea.Cmd
		s.name, cmd = s.name.Update(k)
		return cmd
	case isUp(key):
		return s.setFocus(s.focus - 1)
	case isDown(key):
		return s.setFocus(s.focus + 1)
	case key == "enter" || key == " ":
		switch s.focus {
		case 1:
			s.withData = !s.withData
		case 2:
			s.fk = !s.fk
		case 3:
			s.drop = !s.drop
		case 4:
			return s.generate(a)
		case 5:
			a.pop()
		}
	}
	return nil
}

func (s *convertNew) generate(a *App) tea.Cmd {
	name := strings.TrimSpace(s.name.Value())
	if !migrate.ValidDatabaseName(name) {
		s.err = "El nombre solo puede tener letras, números, _ o $ (hasta 64 caracteres)."
		return nil
	}
	s.busy, s.err = true, ""
	opts := migrate.NewOptions{Database: name, Data: s.withData, ForeignKeys: s.fk, DropDatabase: s.drop}
	sess := a.sess
	return previewCmd(s.id, name+".sql", func(w io.Writer) error {
		_, err := sess.ScriptNew(context.Background(), w, opts)
		return err
	})
}

func (s *convertNew) view(a *App, cw int) string {
	label := padRight("Nombre de la base", 19)
	nameRow := "   " + label + " " + s.name.View()
	if s.focus == 0 {
		nameRow = sSelMark.Render(" ▸ ") + sSel.Render(label) + " " + s.name.View()
	}
	gen := "Generar script SQL"
	if s.busy {
		gen = "Generando…"
	}
	errLine := ""
	if s.err != "" {
		errLine = "   " + sErr.Render("✖ "+clip(s.err, cw-6))
	}
	return strings.Join([]string{
		nameRow,
		"",
		row(check(s.withData, "Incluir los datos"), "", s.focus == 1, false, cw),
		"     " + sMuted.Render("Si lo desmarcás, solo se crea la estructura de las tablas."),
		row(check(s.fk, "Incluir las relaciones"), "", s.focus == 2, false, cw),
		"     " + sMuted.Render("FOREIGN KEY de las relaciones con integridad referencial en Access."),
		row(check(s.drop, "Borrar la base si ya existe"), "", s.focus == 3, false, cw),
		"     " + sMuted.Render("Agrega DROP DATABASE IF EXISTS. Cuidado: elimina lo que haya."),
		errLine,
		row(gen, "", s.focus == 4, false, cw),
		row("Volver", "", s.focus == 5, false, cw),
	}, "\n")
}

// ---------------------------------------------------------------------------
// Agregar datos a tablas que ya existen
// ---------------------------------------------------------------------------

// Filas planas: las tablas (0..n-1), y después duplicados, transacción,
// generar y volver.
type convertAdd struct {
	id     int
	tables []engine.TableInfo
	sel    []bool
	dst    []string
	cols   []string // columnas de destino, como las escribió el usuario ("" = todas)
	dup    int
	tx     bool
	cur    int
	top    int
	busy   bool
	err    string

	editing int // 0 no · 1 destino · 2 columnas
	edit    textinput.Model
}

const addRows = 11

var dupLabels = []string{"Omitirlo (no cambia nada)", "Actualizarlo con los datos de Access", "Reemplazarlo por completo"}

func newConvertAdd(a *App) (*convertAdd, error) {
	tables, err := a.sess.Tables()
	if err != nil {
		return nil, err
	}
	s := &convertAdd{
		id: nextID(), tables: tables, tx: true,
		sel: make([]bool, len(tables)), dst: make([]string, len(tables)), cols: make([]string, len(tables)),
	}
	for i, t := range tables {
		s.sel[i], s.dst[i] = true, t.Name
	}
	s.edit = newFieldInput("", 40)
	return s, nil
}

func (s *convertAdd) crumb() string { return "Convertir a MySQL › Agregar a tablas existentes" }
func (s *convertAdd) help() string {
	if s.editing != 0 {
		return "Enter aceptar · Esc cancelar"
	}
	return "Espacio marcar · e destino · c columnas · a/n todas/ninguna · Esc volver"
}

func (s *convertAdd) total() int   { return len(s.tables) + 4 }
func (s *convertAdd) rowDup() int  { return len(s.tables) }
func (s *convertAdd) rowTx() int   { return len(s.tables) + 1 }
func (s *convertAdd) rowGen() int  { return len(s.tables) + 2 }
func (s *convertAdd) rowBack() int { return len(s.tables) + 3 }

func (s *convertAdd) move(d int) {
	s.cur = (s.cur + d + s.total()) % s.total()
	if s.cur < s.top {
		s.top = s.cur
	}
	if s.cur >= s.top+addRows {
		s.top = s.cur - addRows + 1
	}
	s.top = max(0, s.top)
}

func (s *convertAdd) markAll(on bool) {
	for i := range s.sel {
		s.sel[i] = on
	}
}

func splitColumns(text string) []string {
	var out []string
	for _, c := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func (s *convertAdd) update(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case scriptReady:
		if m.id != s.id {
			return nil
		}
		s.busy = false
		if m.err != nil {
			s.err = oneLine(m.err.Error())
			return nil
		}
		s.err = ""
		return a.push(newScriptScreen(m))
	case tea.KeyMsg:
		return s.key(a, m)
	}
	if s.editing != 0 {
		var cmd tea.Cmd
		s.edit, cmd = s.edit.Update(msg)
		return cmd
	}
	return nil
}

func (s *convertAdd) key(a *App, k tea.KeyMsg) tea.Cmd {
	key := k.String()
	if s.busy {
		return nil
	}
	if s.editing != 0 {
		switch key {
		case "enter":
			v := strings.TrimSpace(s.edit.Value())
			if s.editing == 1 {
				s.dst[s.cur] = v
			} else {
				s.cols[s.cur] = v
			}
			s.editing = 0
			s.edit.Blur()
		case "esc":
			s.editing = 0
			s.edit.Blur()
		default:
			var cmd tea.Cmd
			s.edit, cmd = s.edit.Update(k)
			return cmd
		}
		return nil
	}

	n := len(s.tables)
	switch {
	case key == "esc":
		a.pop()
	case isUp(key):
		s.move(-1)
	case isDown(key):
		s.move(1)
	case key == "pgup":
		s.move(-addRows)
	case key == "pgdown":
		s.move(addRows)
	case key == "a":
		s.markAll(true)
	case key == "n":
		s.markAll(false)
	case key == "e" && s.cur < n:
		s.edit.SetValue(s.dst[s.cur])
		s.editing = 1
		return s.edit.Focus()
	case key == "c" && s.cur < n:
		s.edit.SetValue(s.cols[s.cur])
		s.editing = 2
		return s.edit.Focus()
	case (key == "left" || key == "right" || key == "h" || key == "l") && s.cur == s.rowDup():
		d := 1
		if key == "left" || key == "h" {
			d = -1
		}
		s.dup = (s.dup + d + len(dupLabels)) % len(dupLabels)
	case key == "enter" || key == " ":
		switch {
		case s.cur < n:
			s.sel[s.cur] = !s.sel[s.cur]
		case s.cur == s.rowDup():
			s.dup = (s.dup + 1) % len(dupLabels)
		case s.cur == s.rowTx():
			s.tx = !s.tx
		case s.cur == s.rowGen():
			return s.generate(a)
		case s.cur == s.rowBack():
			a.pop()
		}
	}
	return nil
}

func (s *convertAdd) generate(a *App) tea.Cmd {
	var pairs []migrate.TablePair
	for i, t := range s.tables {
		if s.sel[i] {
			pairs = append(pairs, migrate.TablePair{Source: t.Name, Target: s.dst[i], TargetColumns: splitColumns(s.cols[i])})
		}
	}
	if len(pairs) == 0 {
		a.alert("Marcá al menos una tabla para migrar.")
		return nil
	}
	s.busy, s.err = true, ""
	opts := migrate.AddOptions{Tables: pairs, OnDuplicate: migrate.Duplicate(s.dup), Transaction: s.tx}
	sess := a.sess
	name := strings.TrimSuffix(baseName(a.sess.State().File), "."+extOf(a.sess.State().File)) + "-insert.sql"
	return previewCmd(s.id, name, func(w io.Writer) error {
		_, err := sess.ScriptAdd(context.Background(), w, opts)
		return err
	})
}

func extOf(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return ""
}

func (s *convertAdd) view(a *App, cw int) string {
	count := 0
	for _, v := range s.sel {
		if v {
			count++
		}
	}
	head := sMuted.Render(fmt.Sprintf("%d de %d tablas marcadas. Si la de MySQL tiene menos columnas, usá c.", count, len(s.tables)))
	L := []string{clip(head, cw)}
	end := min(s.total(), s.top+addRows)
	for i := s.top; i < end; i++ {
		sel := i == s.cur
		switch {
		case i < len(s.tables):
			t := s.tables[i]
			if s.editing != 0 && sel {
				what := "→"
				if s.editing == 2 {
					what = "columnas:"
				}
				L = append(L, sSelMark.Render(" ▸ ")+sSel.Render(clip(check(s.sel[i], t.Name)+" "+what, 28))+" "+s.edit.View())
				continue
			}
			hint := "→ " + s.dst[i]
			if c := splitColumns(s.cols[i]); len(c) > 0 {
				hint += fmt.Sprintf("  (%d col.)", len(c))
			}
			L = append(L, row(check(s.sel[i], t.Name), hint, sel, false, cw))
		case i == s.rowDup():
			L = append(L, row("Si el registro ya existe:  ‹ "+dupLabels[s.dup]+" ›", "", sel, false, cw))
		case i == s.rowTx():
			L = append(L, row(check(s.tx, "Envolver en una transacción"), "", sel, false, cw))
		case i == s.rowGen():
			gen := "Generar script SQL"
			switch {
			case s.busy:
				gen = "Generando…"
			case s.err != "":
				gen += "  ✖ " + clip(s.err, 40)
			}
			L = append(L, row(gen, "", sel, false, cw))
		default:
			L = append(L, row("Volver", "", sel, false, cw))
		}
	}
	return strings.Join(L, "\n")
}

var _ = session.ErrNoImage // la pantalla depende de que haya imagen: Tables() lo informa
