package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"access2mysql/internal/engine"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fakeDB es un motor falso: devuelve resultados armados a mano.
type fakeDB struct {
	tables  []string
	lastSQL string
	handler func(sql string, max int) (*engine.Result, bool, error)
}

func (f *fakeDB) Tables() ([]string, error) { return f.tables, nil }
func (f *fakeDB) QueryN(q string, max int) (*engine.Result, bool, error) {
	f.lastSQL = q
	return f.handler(q, max)
}

func wideResult(cols, rows int) *engine.Result {
	r := &engine.Result{Columns: make([]string, cols), Rows: make([][]any, rows)}
	for c := range r.Columns {
		r.Columns[c] = fmt.Sprintf("columna_%02d", c)
	}
	for i := range r.Rows {
		row := make([]any, cols)
		for c := range row {
			row[c] = fmt.Sprintf("fila%03d-col%02d", i, c)
		}
		row[0] = i
		r.Rows[i] = row
	}
	return r
}

func newTestModel(t *testing.T, w, h int) (Model, *fakeDB) {
	t.Helper()
	db := &fakeDB{
		tables: []string{"Clientes", "Pedidos", "Productos con un nombre largo largo largo"},
		handler: func(q string, max int) (*engine.Result, bool, error) {
			return wideResult(25, 300), false, nil
		},
	}
	m, err := New(db, "Northwind.accdb")
	if err != nil {
		t.Fatal(err)
	}
	return send(m, tea.WindowSizeMsg{Width: w, Height: h}), db
}

func send(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// press envía una tecla; si dispara una consulta, ejecuta su tea.Cmd y le
// devuelve el resultado al modelo, como haría bubbletea.
func press(t *testing.T, m Model, k string) Model {
	t.Helper()
	next, cmd := m.Update(key(k))
	m = next.(Model)
	if m.running && cmd != nil {
		msg := cmd()
		if _, ok := msg.(queryDoneMsg); !ok {
			t.Fatalf("la consulta devolvió %T, se esperaba queryDoneMsg", msg)
		}
		m = send(m, msg)
	}
	return m
}

// assertFits verifica que la pantalla no se desborde del tamaño de la terminal.
func assertFits(t *testing.T, label, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("%s: %d líneas, la terminal tiene %d", label, len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			t.Errorf("%s: la línea %d mide %d celdas, la terminal %d: %q", label, i, lw, w, l)
			return
		}
	}
}

func TestLayoutFitsAtAnySize(t *testing.T) {
	sizes := [][2]int{{60, 16}, {61, 17}, {80, 24}, {100, 30}, {120, 40}, {200, 60}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		m, _ := newTestModel(t, w, h)
		assertFits(t, fmt.Sprintf("%dx%d inicial", w, h), m.View(), w, h)

		m = press(t, m, "enter") // vista previa de la primera tabla (resultado ancho)
		assertFits(t, fmt.Sprintf("%dx%d con resultado", w, h), m.View(), w, h)

		for i := 0; i < 8; i++ {
			m = press(t, m, "right")
			m = press(t, m, "pgdown")
		}
		assertFits(t, fmt.Sprintf("%dx%d scrolleado", w, h), m.View(), w, h)

		m = send(m, queryDoneMsg{err: errors.New("línea uno\nlínea dos " + strings.Repeat("larga ", 60))})
		assertFits(t, fmt.Sprintf("%dx%d con error largo", w, h), m.View(), w, h)
	}
}

func TestTinyTerminalShowsMessage(t *testing.T) {
	m, _ := newTestModel(t, 40, 10)
	if v := m.View(); !strings.Contains(v, "muy chica") {
		t.Errorf("se esperaba aviso de terminal chica, hay: %q", v)
	}
}

func TestEnterOnTablePreviewsIt(t *testing.T) {
	m, db := newTestModel(t, 100, 30)
	m = press(t, m, "down") // Pedidos
	m = press(t, m, "enter")
	if db.lastSQL != "SELECT * FROM `Pedidos` LIMIT 100" {
		t.Errorf("SQL inesperado: %q", db.lastSQL)
	}
	if m.focus != focusGrid {
		t.Errorf("tras ejecutar, el foco debería estar en la grilla")
	}
	if v := m.View(); !strings.Contains(v, "fila000-col01") || !strings.Contains(v, "300 filas") {
		t.Errorf("la pantalla no muestra el resultado:\n%s", v)
	}
}

func TestDescribeKey(t *testing.T) {
	m, db := newTestModel(t, 100, 30)
	press(t, m, "s")
	if db.lastSQL != "DESCRIBE `Clientes`" {
		t.Errorf("SQL inesperado: %q", db.lastSQL)
	}
}

func TestGridScrollsBothWays(t *testing.T) {
	m, _ := newTestModel(t, 100, 30)
	m = press(t, m, "enter")
	if strings.Contains(m.View(), "columna_20") {
		t.Fatal("la columna 20 no debería caber en pantalla al inicio")
	}
	for i := 0; i < 22; i++ {
		m = press(t, m, "right")
	}
	if !strings.Contains(m.View(), "columna_22") {
		t.Errorf("tras desplazar a la derecha debería verse la columna 22")
	}
	m = press(t, m, "end")
	if m.grid.cur != 299 {
		t.Errorf("end debería ir a la última fila (299), está en %d", m.grid.cur)
	}
	if !strings.Contains(m.View(), "fila299") {
		t.Errorf("la última fila debería estar visible")
	}
}

func TestEditorRunsTypedSQL(t *testing.T) {
	m, db := newTestModel(t, 100, 30)
	m = send(m, key("tab")) // → editor
	if m.focus != focusEditor {
		t.Fatal("tab debería llevar al editor")
	}
	for _, r := range "SELECT 1" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = press(t, m, "ctrl+r")
	if db.lastSQL != "SELECT 1" {
		t.Errorf("SQL inesperado: %q", db.lastSQL)
	}
}

func TestEmptySQLIsAnError(t *testing.T) {
	m, db := newTestModel(t, 100, 30)
	m = send(m, key("ctrl+r"))
	if !m.statusErr || db.lastSQL != "" {
		t.Errorf("una consulta vacía no debería ejecutarse (statusErr=%v sql=%q)", m.statusErr, db.lastSQL)
	}
}

func TestQueryErrorIsShownAndKeepsPreviousResult(t *testing.T) {
	m, db := newTestModel(t, 100, 30)
	m = press(t, m, "enter")
	db.handler = func(string, int) (*engine.Result, bool, error) { return nil, false, errors.New("table not found: x") }
	m = press(t, m, "ctrl+r") // vuelve a ejecutar lo que quedó en el editor
	v := m.View()
	if !strings.Contains(v, "table not found: x") {
		t.Errorf("el error debería verse")
	}
	if !strings.Contains(v, "fila000-col01") {
		t.Errorf("el resultado anterior debería seguir visible tras un error")
	}
}

func TestEnginePanicBecomesError(t *testing.T) {
	m, db := newTestModel(t, 100, 30)
	db.handler = func(string, int) (*engine.Result, bool, error) { panic("boom") }
	m = press(t, m, "enter")
	if !m.statusErr || !strings.Contains(m.status, "boom") {
		t.Errorf("un pánico del motor debería mostrarse como error, status=%q", m.status)
	}
	if m.running {
		t.Errorf("tras el error no debería quedar 'ejecutando'")
	}
}

func TestTruncatedResultIsFlagged(t *testing.T) {
	m, db := newTestModel(t, 140, 30)
	db.handler = func(string, int) (*engine.Result, bool, error) { return wideResult(3, 1000), true, nil }
	m = press(t, m, "enter")
	if !strings.Contains(m.View(), "SOLO LAS PRIMERAS 1000") {
		t.Errorf("debería avisar que el resultado está truncado")
	}
}

func TestSecondRunIgnoredWhileRunning(t *testing.T) {
	m, _ := newTestModel(t, 100, 30)
	next, cmd := m.Update(key("enter"))
	m = next.(Model)
	if cmd == nil || !m.running {
		t.Fatal("la primera ejecución debería lanzar una consulta")
	}
	_, cmd2 := m.Update(key("ctrl+r"))
	if cmd2 != nil {
		t.Errorf("mientras corre una consulta, otra no debería lanzarse")
	}
}

func TestSaveWritesOrderedJSON(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	m, db := newTestModel(t, 100, 30)
	m = send(m, key("ctrl+s"))
	if !m.statusErr {
		t.Error("guardar sin resultado debería avisar")
	}
	db.handler = func(string, int) (*engine.Result, bool, error) {
		return &engine.Result{Columns: []string{"zeta", "alfa"}, Rows: [][]any{{1, "x"}, {2, nil}}}, false, nil
	}
	m = press(t, m, "enter")
	m = send(m, key("ctrl+s"))
	if m.statusErr {
		t.Fatalf("no debería fallar: %s", m.status)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("se esperaba 1 archivo, hay %d", len(files))
	}
	data, _ := os.ReadFile(files[0].Name())
	if !json.Valid(data) {
		t.Fatalf("JSON inválido: %s", data)
	}
	if strings.Index(string(data), `"zeta"`) > strings.Index(string(data), `"alfa"`) {
		t.Errorf("el orden de columnas no se conservó:\n%s", data)
	}
}

func TestFocusCycles(t *testing.T) {
	m, _ := newTestModel(t, 100, 30)
	seen := []focus{m.focus}
	for i := 0; i < 3; i++ {
		m = send(m, key("tab"))
		seen = append(seen, m.focus)
	}
	if seen[0] != focusTables || seen[1] != focusEditor || seen[2] != focusGrid || seen[3] != focusTables {
		t.Errorf("orden de foco inesperado: %v", seen)
	}
}

func TestCellTextIsSingleLine(t *testing.T) {
	for _, v := range []any{"a\nb", "a\r\nb\tc", nil, []byte{1, 2, 3}, 3.5, true} {
		if s := cellText(v); strings.ContainsAny(s, "\n\r\t") {
			t.Errorf("cellText(%#v) = %q contiene saltos", v, s)
		}
	}
	if cellText(nil) != "NULL" || cellText([]byte{1, 2, 3}) != "<3 bytes>" {
		t.Error("formato de NULL o binarios inesperado")
	}
}
