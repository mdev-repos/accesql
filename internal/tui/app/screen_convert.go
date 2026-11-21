package app

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func newFieldInput(val string, w int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 64
	ti.Width = w
	ti.TextStyle = sField
	ti.SetValue(val)
	return ti
}

// ---------------------------------------------------------------------------
// Crear una base de datos nueva
// ---------------------------------------------------------------------------

// Filas: 0 nombre · 1 incluir datos · 2 borrar si existe · 3 generar · 4 volver.
type convertNew struct {
	name     textinput.Model
	withData bool
	drop     bool
	focus    int
}

var nonIdent = regexp.MustCompile(`[^a-z0-9_]+`)

func newConvertNew(a *App) *convertNew {
	base := strings.TrimSuffix(baseName(a.st.filePath), ".accdb")
	base = strings.TrimSuffix(base, ".mdb")
	def := strings.Trim(nonIdent.ReplaceAllString(strings.ToLower(base), "_"), "_")
	if def == "" {
		def = "mi_base"
	}
	s := &convertNew{name: newFieldInput(def, 28), withData: true}
	return s
}

// init da el foco al campo del nombre apenas se abre la pantalla.
func (s *convertNew) init() tea.Cmd { return s.name.Focus() }

func (s *convertNew) crumb() string { return "Convertir a MySQL › Base nueva" }
func (s *convertNew) help() string  { return "↑/↓ mover · Enter/Espacio elegir · Esc volver" }

func (s *convertNew) setFocus(i int) tea.Cmd {
	s.focus = (i + 5) % 5
	s.name.Blur()
	if s.focus == 0 {
		return s.name.Focus()
	}
	return nil
}

func (s *convertNew) update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		if s.focus == 0 {
			var cmd tea.Cmd
			s.name, cmd = s.name.Update(msg)
			return cmd
		}
		return nil
	}
	key := k.String()
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
		s.name, cmd = s.name.Update(msg)
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
			s.drop = !s.drop
		case 3:
			name := strings.TrimSpace(s.name.Value())
			if !dbNameRe.MatchString(name) {
				a.alert("El nombre solo puede tener letras, números y _.")
				return nil
			}
			return a.push(newScriptScreen(mockScriptNew(baseName(a.st.filePath), name, s.withData, s.drop)))
		case 4:
			a.pop()
		}
	}
	return nil
}

func (s *convertNew) view(a *App, cw int) string {
	label := padRight("Nombre de la base", 19)
	var nameRow string
	if s.focus == 0 {
		nameRow = sSelMark.Render(" ▸ ") + sSel.Render(label) + " " + s.name.View()
	} else {
		nameRow = "   " + label + " " + s.name.View()
	}
	return strings.Join([]string{
		nameRow,
		"",
		row(check(s.withData, "Incluir los datos"), "", s.focus == 1, false, cw),
		"     " + sMuted.Render("Si lo desmarcás, solo se crea la estructura de las tablas."),
		row(check(s.drop, "Borrar la base si ya existe"), "", s.focus == 2, false, cw),
		"     " + sMuted.Render("Agrega DROP DATABASE IF EXISTS. Cuidado: elimina lo que haya."),
		"",
		row("Generar script SQL", "", s.focus == 3, false, cw),
		row("Volver", "", s.focus == 4, false, cw),
	}, "\n")
}

// ---------------------------------------------------------------------------
// Agregar datos a tablas que ya existen
// ---------------------------------------------------------------------------

// Filas planas: tablas (0..n-1), luego duplicados, transacción, generar, volver.
type convertAdd struct {
	sel     []bool
	dst     []string
	dup     int
	tx      bool
	cur     int
	top     int
	editing bool
	edit    textinput.Model
}

const addRows = 11

func newConvertAdd() *convertAdd {
	n := len(mockTables)
	s := &convertAdd{sel: make([]bool, n), dst: make([]string, n), tx: true}
	for i, t := range mockTables {
		s.sel[i] = true
		s.dst[i] = t.name
	}
	s.edit = newFieldInput("", 26)
	return s
}

func (s *convertAdd) crumb() string { return "Convertir a MySQL › Agregar a tablas existentes" }
func (s *convertAdd) help() string {
	if s.editing {
		return "Enter aceptar · Esc cancelar"
	}
	return "↑/↓ mover · Espacio marcar · e renombrar · a/n todas/ninguna · Esc volver"
}

func (s *convertAdd) total() int   { return len(mockTables) + 4 }
func (s *convertAdd) rowDup() int  { return len(mockTables) }
func (s *convertAdd) rowTx() int   { return len(mockTables) + 1 }
func (s *convertAdd) rowGen() int  { return len(mockTables) + 2 }
func (s *convertAdd) rowBack() int { return len(mockTables) + 3 }

func (s *convertAdd) move(d int) {
	s.cur = (s.cur + d + s.total()) % s.total()
	if s.cur < s.top {
		s.top = s.cur
	}
	if s.cur >= s.top+addRows {
		s.top = s.cur - addRows + 1
	}
	if s.cur < addRows && s.top > 0 && s.cur < s.top {
		s.top = 0
	}
}

func (s *convertAdd) markAll(on bool) {
	for i := range s.sel {
		s.sel[i] = on
	}
}

func (s *convertAdd) update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		if s.editing {
			var cmd tea.Cmd
			s.edit, cmd = s.edit.Update(msg)
			return cmd
		}
		return nil
	}
	key := k.String()

	if s.editing {
		switch key {
		case "enter":
			s.dst[s.cur] = strings.TrimSpace(s.edit.Value())
			s.editing = false
			s.edit.Blur()
		case "esc":
			s.editing = false
			s.edit.Blur()
		default:
			var cmd tea.Cmd
			s.edit, cmd = s.edit.Update(msg)
			return cmd
		}
		return nil
	}

	n := len(mockTables)
	switch {
	case key == "esc":
		a.pop()
	case isUp(key):
		s.move(-1)
	case isDown(key):
		s.move(1)
	case key == "a":
		s.markAll(true)
	case key == "n":
		s.markAll(false)
	case key == "e" && s.cur < n:
		s.edit.SetValue(s.dst[s.cur])
		s.editing = true
		return s.edit.Focus()
	case (key == "left" || key == "right" || key == "h" || key == "l") && s.cur == s.rowDup():
		d := 1
		if key == "left" || key == "h" {
			d = -1
		}
		s.dup = (s.dup + d + len(dupModes)) % len(dupModes)
	case key == "enter" || key == " ":
		switch {
		case s.cur < n:
			s.sel[s.cur] = !s.sel[s.cur]
		case s.cur == s.rowDup():
			s.dup = (s.dup + 1) % len(dupModes)
		case s.cur == s.rowTx():
			s.tx = !s.tx
		case s.cur == s.rowGen():
			var src, dst []string
			for i, t := range mockTables {
				if s.sel[i] {
					src = append(src, t.name)
					dst = append(dst, s.dst[i])
				}
			}
			if len(src) == 0 {
				a.alert("Marcá al menos una tabla para migrar.")
				return nil
			}
			return a.push(newScriptScreen(mockScriptAdd(baseName(a.st.filePath), src, dst, s.dup, s.tx)))
		case s.cur == s.rowBack():
			a.pop()
		}
	}
	return nil
}

func (s *convertAdd) view(a *App, cw int) string {
	count := 0
	for _, v := range s.sel {
		if v {
			count++
		}
	}
	L := []string{
		sMuted.Render(fmt.Sprintf("Las columnas se emparejan por nombre. %d de %d tablas marcadas.", count, len(mockTables))),
	}
	end := min(s.total(), s.top+addRows)
	for i := s.top; i < end; i++ {
		sel := i == s.cur
		switch {
		case i < len(mockTables):
			dst := s.dst[i]
			if s.editing && sel {
				L = append(L, sSelMark.Render(" ▸ ")+sSel.Render(padRight(check(s.sel[i], mockTables[i].name+" →"), cw-3-fieldW-1))+" "+s.edit.View())
				continue
			}
			label := check(s.sel[i], mockTables[i].name)
			hint := ""
			if dst != mockTables[i].name {
				hint = "→ " + dst
			} else {
				hint = "→ " + dst
			}
			L = append(L, row(label, hint, sel, false, cw))
		case i == s.rowDup():
			L = append(L, row("Si el registro ya existe:  ‹ "+dupModes[s.dup]+" ›", "", sel, false, cw))
		case i == s.rowTx():
			L = append(L, row(check(s.tx, "Envolver en una transacción"), "", sel, false, cw))
		case i == s.rowGen():
			L = append(L, row("Generar script SQL", "", sel, false, cw))
		default:
			L = append(L, row("Volver", "", sel, false, cw))
		}
	}
	return strings.Join(L, "\n")
}
