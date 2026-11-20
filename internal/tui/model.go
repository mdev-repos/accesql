// Package tui es la interfaz de terminal interactiva (bubbletea): explorar las
// tablas, escribir consultas SQL y ver los resultados sin tipear comandos.
//
// bubbletea usa la "arquitectura Elm": todo el estado vive en Model, Update
// recibe cada evento (tecla, cambio de tamaño, consulta terminada) y devuelve
// el modelo nuevo, y View dibuja la pantalla completa a partir del modelo.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"access2mysql/internal/engine"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// maxRows es el tope de filas que se traen a pantalla por consulta.
const maxRows = 1000

// Runner es lo que la TUI necesita del motor. Es una interfaz para poder
// probarla con un motor falso.
type Runner interface {
	Tables() ([]string, error)
	QueryN(q string, max int) (*engine.Result, bool, error)
}

type focus int

const (
	focusTables focus = iota
	focusEditor
	focusGrid
	numFocus
)

var (
	bold     = lipgloss.NewStyle().Bold(true)
	selected = lipgloss.NewStyle().Reverse(true)
	dim      = lipgloss.NewStyle().Faint(true)
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	okStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)

// queryDoneMsg llega cuando termina una consulta lanzada en segundo plano.
type queryDoneMsg struct {
	res       *engine.Result
	truncated bool
	err       error
	took      time.Duration
}

// Model es el estado completo de la pantalla.
type Model struct {
	db       Runner
	fileName string

	tables  []string
	tcur    int // tabla seleccionada
	toff    int // primera tabla visible
	editor  textarea.Model
	grid    *grid
	focus   focus
	running bool

	resSummary string
	status     string
	statusErr  bool

	w, h int
	// result es el último resultado, para guardarlo en JSON.
	result *engine.Result
}

// New crea el modelo. fileName es solo para mostrar en el título.
func New(db Runner, fileName string) (Model, error) {
	tables, err := db.Tables()
	if err != nil {
		return Model{}, err
	}
	ed := textarea.New()
	ed.ShowLineNumbers = false
	ed.Prompt = " " // un espacio de margen respecto del borde
	ed.Placeholder = "SELECT * FROM ...   (Ctrl+R para ejecutar)"
	ed.CharLimit = 0
	ed.SetHeight(4)
	return Model{
		db: db, fileName: fileName, tables: tables, editor: ed,
		grid:   newGrid(nil, nil),
		status: fileName + " · Enter: ver tabla · s: estructura · Tab: cambiar panel · Ctrl+R: ejecutar SQL",
	}, nil
}

// Init implementa tea.Model. No hay nada que hacer al arrancar.
func (m Model) Init() tea.Cmd { return nil }

// Update implementa tea.Model: recibe un evento y devuelve el modelo nuevo.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.editor.SetWidth(max(10, m.rightWidth()-2))
		return m, nil

	case queryDoneMsg:
		m.running = false
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.result = msg.res
		m.grid = newGrid(msg.res.Columns, msg.res.Rows)
		m.resSummary = fmt.Sprintf("%d filas · %d columnas · %s", len(msg.res.Rows), len(msg.res.Columns), formatDuration(msg.took))
		if msg.truncated {
			m.resSummary += fmt.Sprintf(" · MOSTRANDO SOLO LAS PRIMERAS %d", maxRows)
		}
		m.setStatus("Listo. ←→ ↑↓ para recorrer, Ctrl+S guarda el resultado, Tab vuelve al editor.", false)
		return m.setFocus(focusGrid)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	// Mensajes internos (parpadeo del cursor, etc.): el editor los necesita.
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	return m, cmd
}

func (m *Model) setStatus(s string, isErr bool) { m.status, m.statusErr = s, isErr }

func (m Model) setFocus(f focus) (tea.Model, tea.Cmd) {
	m.focus = f
	if f == focusEditor {
		return m, m.editor.Focus()
	}
	m.editor.Blur()
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		return m.setFocus((m.focus + 1) % numFocus)
	case "shift+tab":
		return m.setFocus((m.focus + numFocus - 1) % numFocus)
	case "ctrl+r":
		return m.run(m.editor.Value())
	case "ctrl+s":
		return m.save()
	}

	switch m.focus {
	case focusTables:
		switch msg.String() {
		case "up", "k":
			m.tcur = max(0, m.tcur-1)
		case "down", "j":
			m.tcur = min(len(m.tables)-1, m.tcur+1)
		case "home", "g":
			m.tcur = 0
		case "end", "G":
			m.tcur = max(0, len(m.tables)-1)
		case "enter":
			if t := m.currentTable(); t != "" {
				m.editor.SetValue(fmt.Sprintf("SELECT * FROM `%s` LIMIT 100", t))
				return m.run(m.editor.Value())
			}
		case "s":
			if t := m.currentTable(); t != "" {
				m.editor.SetValue(fmt.Sprintf("DESCRIBE `%s`", t))
				return m.run(m.editor.Value())
			}
		case "q":
			return m, tea.Quit
		}
	case focusEditor:
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd
	case focusGrid:
		page := m.gridInnerHeight() - 1
		switch msg.String() {
		case "up", "k":
			m.grid.move(-1, 0, page)
		case "down", "j":
			m.grid.move(1, 0, page)
		case "pgup":
			m.grid.move(-page, 0, page)
		case "pgdown":
			m.grid.move(page, 0, page)
		case "home", "g":
			m.grid.move(-len(m.grid.rows), 0, page)
		case "end", "G":
			m.grid.move(len(m.grid.rows), 0, page)
		case "left", "h":
			m.grid.move(0, -1, page)
		case "right", "l":
			m.grid.move(0, 1, page)
		case "q":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m Model) currentTable() string {
	if m.tcur < 0 || m.tcur >= len(m.tables) {
		return ""
	}
	return m.tables[m.tcur]
}

// run lanza la consulta en segundo plano. La función que devuelve es un
// tea.Cmd: bubbletea la ejecuta en otra goroutine y entrega su resultado como
// un mensaje, para que la pantalla no se congele mientras tanto.
func (m Model) run(sqlText string) (tea.Model, tea.Cmd) {
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		m.setStatus("Escribí una consulta SQL en el editor.", true)
		return m, nil
	}
	if m.running {
		return m, nil // una a la vez: el motor comparte una sesión
	}
	m.running = true
	m.setStatus("Ejecutando...", false)
	db := m.db
	return m, func() (out tea.Msg) {
		start := time.Now()
		// El motor SQL podría entrar en pánico con una consulta rara; que eso
		// sea un mensaje de error y no cierre la interfaz.
		defer func() {
			if r := recover(); r != nil {
				out = queryDoneMsg{err: fmt.Errorf("error interno del motor SQL: %v", r)}
			}
		}()
		res, truncated, err := db.QueryN(sqlText, maxRows)
		return queryDoneMsg{res: res, truncated: truncated, err: err, took: time.Since(start)}
	}
}

// save guarda el último resultado como JSON en la carpeta actual.
func (m Model) save() (tea.Model, tea.Cmd) {
	if m.result == nil {
		m.setStatus("No hay ningún resultado para guardar.", true)
		return m, nil
	}
	data, err := json.MarshalIndent(m.result, "", "  ")
	if err != nil {
		m.setStatus("No se pudo armar el JSON: "+err.Error(), true)
		return m, nil
	}
	name := "resultado-" + time.Now().Format("20060102-150405") + ".json"
	if err := os.WriteFile(name, append(data, '\n'), 0o644); err != nil {
		m.setStatus("No se pudo guardar: "+err.Error(), true)
		return m, nil
	}
	m.setStatus(fmt.Sprintf("Guardado en %s (%d filas)", name, len(m.result.Rows)), false)
	return m, nil
}

// --- Dibujo -------------------------------------------------------------

const (
	minW, minH   = 60, 16
	editorBoxH   = 7 // borde(2) + título(1) + editor(4)
	footerLines  = 2
	leftMinWidth = 18
)

func (m Model) leftWidth() int  { return min(34, max(leftMinWidth, m.w/4)) }
func (m Model) rightWidth() int { return m.w - m.leftWidth() }

func (m Model) bodyHeight() int { return m.h - footerLines }

// gridInnerHeight es el alto útil de la grilla, sin borde ni título.
func (m Model) gridInnerHeight() int { return m.bodyHeight() - editorBoxH - 2 - 1 }

// View implementa tea.Model: dibuja la pantalla completa.
func (m Model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.w < minW || m.h < minH {
		return fmt.Sprintf("Terminal muy chica (%dx%d). Se necesita al menos %dx%d.", m.w, m.h, minW, minH)
	}

	left := m.box(m.leftWidth(), m.bodyHeight(), m.focus == focusTables, m.tablesView())
	right := lipgloss.JoinVertical(lipgloss.Left,
		m.box(m.rightWidth(), editorBoxH, m.focus == focusEditor, m.editorView()),
		m.box(m.rightWidth(), m.bodyHeight()-editorBoxH, m.focus == focusGrid, m.gridView()),
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	st := m.status
	switch {
	case m.statusErr:
		st = errStyle.Render("Error: " + oneLine(st))
	case m.running:
		st = okStyle.Render(st)
	}
	status := fitLine(st, m.w)
	help := dim.Render(fitLine(" Tab: panel · Ctrl+R: ejecutar · Ctrl+S: guardar JSON · Ctrl+C: salir ", m.w))
	return body + "\n" + status + "\n" + help
}

// box dibuja un panel con borde de tamaño total w x h. El contenido ya viene
// recortado al tamaño interno (w-2 x h-2).
func (m Model) box(w, h int, focused bool, content string) string {
	color := lipgloss.Color("8")
	if focused {
		color = lipgloss.Color("12")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Width(w - 2).Height(h - 2).MaxWidth(w).MaxHeight(h).
		Render(content)
}

func (m Model) tablesView() string {
	inW, inH := m.leftWidth()-2, m.bodyHeight()-2
	lines := []string{bold.Render(fitLine(fmt.Sprintf(" Tablas (%d)", len(m.tables)), inW))}
	page := inH - 1
	off := m.toff
	if m.tcur < off {
		off = m.tcur
	}
	if m.tcur >= off+page {
		off = m.tcur - page + 1
	}
	for i := 0; i < page; i++ {
		n := off + i
		if n >= len(m.tables) {
			lines = append(lines, strings.Repeat(" ", inW))
			continue
		}
		l := fitLine(" "+m.tables[n], inW)
		if n == m.tcur {
			l = selected.Render(l)
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

func (m Model) editorView() string {
	inW := m.rightWidth() - 2
	title := " SQL (dialecto MySQL; nombres con espacios entre `comillas invertidas`)"
	return bold.Render(fitLine(title, inW)) + "\n" + m.editor.View()
}

func (m Model) gridView() string {
	inW := m.rightWidth() - 2
	title := " Resultado"
	if m.resSummary != "" {
		title += " · " + m.resSummary
	}
	// La grilla se dibuja con un espacio de margen a la izquierda.
	lines := strings.Split(m.grid.render(inW-1, m.gridInnerHeight()), "\n")
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	return bold.Render(fitLine(title, inW)) + "\n" + strings.Join(lines, "\n")
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "<1 ms"
	}
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

func oneLine(s string) string {
	return ansi.Truncate(strings.Join(strings.Fields(s), " "), 400, "…")
}
