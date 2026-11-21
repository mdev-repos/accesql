package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"access2mysql/internal/engine"
	"access2mysql/internal/session"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	maxQueryRows  = 1000      // filas que se traen a pantalla por consulta
	maxExportRows = 1_000_000 // filas máximas de una exportación
)

type queryDone struct {
	id        int
	res       *engine.Result
	truncated bool
	err       error
	took      time.Duration
}

type exportDone struct {
	id   int
	path string
	err  error
}

// Dónde está el foco dentro de la pantalla de consultas.
const (
	focusEditor = iota
	focusButtons
	focusGrid
)

var queryButtons = []string{"Ejecutar", "Tablas", "Limpiar", "JSON", "CSV", "SQL", "Volver"}

// queryScreen: editor SQL, botones y grilla de resultados con scroll.
type queryScreen struct {
	id      int
	ed      textarea.Model
	focus   int
	btn     int
	running bool

	res       *engine.Result
	g         *grid
	truncated bool
	took      time.Duration
	lastSQL   string // la consulta que produjo el resultado (la que se exporta)
	errMsg    string
}

func newQueryScreen(a *App) *queryScreen {
	ed := textarea.New()
	ed.ShowLineNumbers = false
	ed.Prompt = ""
	ed.Placeholder = "SELECT * FROM `Customers` LIMIT 100"
	ed.SetHeight(4)
	ed.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ed.Focus()
	return &queryScreen{id: nextID(), ed: ed}
}

func (s *queryScreen) crumb() string { return "Consultas SQL" }
func (s *queryScreen) help() string {
	switch s.focus {
	case focusEditor:
		return "Ctrl+R ejecutar · Tab botones · Esc botones"
	case focusGrid:
		return "↑/↓ ←/→ mover · Tab editor · Esc botones"
	}
	return "←/→ elegir · Enter aceptar · Tab siguiente · Esc volver"
}

func (s *queryScreen) setEditorFocus(on bool) tea.Cmd {
	if on {
		s.focus = focusEditor
		return s.ed.Focus()
	}
	s.ed.Blur()
	return nil
}

// next pasa el foco al siguiente panel (la grilla solo si hay resultado).
func (s *queryScreen) next() tea.Cmd {
	switch s.focus {
	case focusEditor:
		s.ed.Blur()
		s.focus = focusButtons
	case focusButtons:
		if s.g != nil {
			s.focus = focusGrid
		} else {
			return s.setEditorFocus(true)
		}
	default:
		return s.setEditorFocus(true)
	}
	return nil
}

func (s *queryScreen) update(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case queryDone:
		if m.id != s.id {
			return nil
		}
		s.running = false
		if m.err != nil {
			s.errMsg = oneLine(m.err.Error())
			return nil
		}
		s.errMsg = ""
		s.res, s.truncated, s.took = m.res, m.truncated, m.took
		s.g = newGrid(m.res.Columns, m.res.Rows)
		return nil
	case exportDone:
		if m.id != s.id {
			return nil
		}
		if m.err != nil {
			a.alert("No se pudo exportar: " + oneLine(m.err.Error()))
		} else {
			a.say("Guardado: " + m.path)
		}
		return nil
	case tea.KeyMsg:
		return s.key(a, m)
	}
	var cmd tea.Cmd
	s.ed, cmd = s.ed.Update(msg) // parpadeo del cursor
	return cmd
}

func (s *queryScreen) key(a *App, k tea.KeyMsg) tea.Cmd {
	key := k.String()
	switch s.focus {
	case focusEditor:
		switch key {
		case "ctrl+r":
			return s.run(a)
		case "tab", "esc":
			return s.next()
		}
		var cmd tea.Cmd
		s.ed, cmd = s.ed.Update(k)
		return cmd

	case focusGrid:
		page := s.gridRows() - 1
		switch key {
		case "up", "k":
			s.g.move(-1, 0, page)
		case "down", "j":
			s.g.move(1, 0, page)
		case "left", "h":
			s.g.move(0, -1, page)
		case "right", "l":
			s.g.move(0, 1, page)
		case "pgup":
			s.g.move(-page, 0, page)
		case "pgdown":
			s.g.move(page, 0, page)
		case "home":
			s.g.move(-len(s.g.rows), 0, page)
		case "end":
			s.g.move(len(s.g.rows), 0, page)
		case "ctrl+r":
			return s.run(a)
		case "tab":
			return s.next()
		case "esc":
			s.focus = focusButtons
		}
		return nil
	}

	switch key {
	case "ctrl+r":
		return s.run(a)
	case "left", "h":
		s.btn = (s.btn + len(queryButtons) - 1) % len(queryButtons)
	case "right", "l":
		s.btn = (s.btn + 1) % len(queryButtons)
	case "tab":
		return s.next()
	case "esc":
		a.pop()
	case "enter":
		return s.press(a, queryButtons[s.btn])
	}
	return nil
}

func (s *queryScreen) press(a *App, name string) tea.Cmd {
	switch name {
	case "Ejecutar":
		return s.run(a)
	case "Tablas":
		return a.push(newTablePicker(a, func(table string) {
			s.ed.SetValue("SELECT * FROM `" + strings.ReplaceAll(table, "`", "``") + "` LIMIT 100")
			s.setEditorFocus(true)
		}))
	case "Limpiar":
		s.ed.Reset()
		s.res, s.g, s.errMsg, s.lastSQL = nil, nil, "", ""
		return s.setEditorFocus(true)
	case "JSON":
		return s.export(a, session.FormatJSON)
	case "CSV":
		return s.export(a, session.FormatCSVExcel)
	case "SQL":
		return s.export(a, session.FormatSQL)
	case "Volver":
		a.pop()
	}
	return nil
}

// run lanza la consulta en segundo plano: la función que devuelve es un tea.Cmd,
// que bubbletea ejecuta en otra goroutine y entrega como mensaje, para que la
// pantalla no se congele.
func (s *queryScreen) run(a *App) tea.Cmd {
	q := strings.TrimSpace(s.ed.Value())
	if q == "" {
		a.alert("Escribí una consulta primero.")
		return nil
	}
	if s.running {
		return nil
	}
	s.running, s.errMsg = true, ""
	s.lastSQL = q
	id, sess := s.id, a.sess
	return func() tea.Msg {
		start := time.Now()
		res, trunc, err := sess.Query(q, maxQueryRows)
		return queryDone{id, res, trunc, err, time.Since(start)}
	}
}

// export guarda el resultado de la última consulta en un archivo.
func (s *queryScreen) export(a *App, f session.Format) tea.Cmd {
	if s.res == nil || s.lastSQL == "" {
		a.alert("Primero ejecutá una consulta.")
		return nil
	}
	id, sess, sql := s.id, a.sess, s.lastSQL
	path := filepath.Join(a.saveDir(), "resultado-"+time.Now().Format("20060102-150405")+f.Extension())
	return func() tea.Msg {
		file, err := os.Create(path)
		if err != nil {
			return exportDone{id, "", err}
		}
		err = sess.ExportQuery(sql, f, "resultado", maxExportRows, file)
		if cerr := file.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(path)
		}
		return exportDone{id, path, err}
	}
}

// gridRows es el alto de la grilla (encabezado incluido): lo que sobra del
// cuerpo después del editor con su borde (6), los botones y la línea de estado.
func (s *queryScreen) gridRows() int { return bodyH - 6 - 1 - 1 }

func (s *queryScreen) view(a *App, cw int) string {
	s.ed.SetWidth(cw - 4)
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colMuted).Padding(0, 1)
	if s.focus == focusEditor {
		border = border.BorderForeground(colGold)
	}

	var info string
	switch {
	case s.running:
		info = sMuted.Render("Ejecutando…")
	case s.errMsg != "":
		info = sErr.Render("✖ " + clip(s.errMsg, cw-3))
	case s.res != nil:
		info = sOK.Render("✔ ") + sMuted.Render(fmt.Sprintf("%s filas · %s", thousands(int64(len(s.res.Rows))), fmtTook(s.took)))
		if s.truncated {
			info += sGold.Render(fmt.Sprintf(" · hay más: se muestran las primeras %s", thousands(maxQueryRows)))
		}
	default:
		info = sMuted.Render("El resultado aparecerá acá.")
	}

	L := []string{
		border.Render(s.ed.View()),
		buttons(queryButtons, s.btn, s.focus == focusButtons),
		clip(info, cw),
	}
	if s.g != nil {
		L = append(L, s.g.render(cw, s.gridRows(), s.focus == focusGrid))
	}
	return strings.Join(L, "\n")
}

// fmtTook escribe una duración corta.
func fmtTook(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}
