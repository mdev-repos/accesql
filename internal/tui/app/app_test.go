package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"accesql/internal/session"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const northwind = "../../../testdb/Northwind.accdb"

func TestMain(m *testing.M) {
	openBrowser = func(string) error { return nil } // los tests no abren navegadores
	os.Exit(m.Run())
}

// --- ayudas ----------------------------------------------------------------

var keyTypes = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "up": tea.KeyUp, "down": tea.KeyDown,
	"left": tea.KeyLeft, "right": tea.KeyRight, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
	"backspace": tea.KeyBackspace, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"ctrl+r": tea.KeyCtrlR, "ctrl+c": tea.KeyCtrlC, "home": tea.KeyHome, "end": tea.KeyEnd,
}

func keyMsg(k string) tea.KeyMsg {
	if t, ok := keyTypes[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	if k == "space" {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// press manda una tecla y devuelve el comando que haya pedido la pantalla.
func press(a *App, k string) tea.Cmd {
	_, cmd := a.Update(keyMsg(k))
	return cmd
}

// send manda varias teclas ignorando los comandos (parpadeos de cursor, etc.).
func send(a *App, keys ...string) {
	for _, k := range keys {
		press(a, k)
	}
}

func typeText(a *App, s string) {
	for _, r := range s {
		if r == ' ' {
			send(a, "space")
		} else {
			send(a, string(r))
		}
	}
}

// settle ejecuta cmd (y los comandos que vayan pidiendo las respuestas) hasta
// que done() sea verdadero: simula lo que haría bubbletea con las tareas en
// segundo plano.
func settle(t *testing.T, a *App, cmd tea.Cmd, done func() bool) {
	t.Helper()
	msgs := make(chan tea.Msg, 256)
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, cc := range b {
					run(cc)
				}
				return
			}
			msgs <- msg
		}()
	}
	run(cmd)
	deadline := time.After(40 * time.Second)
	for !done() {
		select {
		case m := <-msgs:
			_, next := a.Update(m)
			run(next)
		case <-deadline:
			t.Fatalf("se agotó el tiempo esperando; pantalla actual:\n%s", plain(a))
		}
	}
}

func sized(a *App, w, h int) *App {
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

func plain(a *App) string { return ansi.Strip(a.View()) }

func needDB(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(northwind); err != nil {
		t.Skip("falta testdb/Northwind.accdb")
	}
}

// copyDB copia la base de prueba a una carpeta temporal: los archivos que
// guarda la interfaz (resultados, scripts) caen junto a la base abierta.
func copyDB(t *testing.T) string {
	t.Helper()
	needDB(t)
	b, err := os.ReadFile(northwind)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "Northwind.accdb")
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// newApp crea la aplicación con una sesión vacía.
func newApp(t *testing.T) (*App, *session.Session) {
	t.Helper()
	sess := session.New()
	a := New(sess)
	t.Cleanup(func() { a.Close(); sess.Close() })
	return sized(a, 80, 24), sess
}

// newOpened crea la aplicación con la base abierta, sin generar la imagen.
func newOpened(t *testing.T) (*App, *session.Session) {
	t.Helper()
	a, sess := newApp(t)
	if err := sess.Open(copyDB(t)); err != nil {
		t.Fatal(err)
	}
	return a, sess
}

// newReady crea la aplicación con la imagen ya generada.
func newReady(t *testing.T) (*App, *session.Session) {
	t.Helper()
	a, sess := newOpened(t)
	if err := sess.Generate(context.Background(), session.GenerateOptions{Database: "northwind"}, nil); err != nil {
		t.Fatal(err)
	}
	return a, sess
}

// goMain mueve el cursor del menú principal hasta la opción id.
func goMain(t *testing.T, a *App, id string) {
	t.Helper()
	s := a.stack[0].(*mainScreen)
	s.refresh(a)
	for i := 0; i < 20 && s.m.current() != id; i++ {
		send(a, "down")
	}
	if s.m.current() != id {
		t.Fatalf("no se pudo llegar a la opción %q", id)
	}
}

// enter abre una opción del menú principal.
func enter(t *testing.T, a *App, id string) tea.Cmd {
	t.Helper()
	goMain(t, a, id)
	n := len(a.stack)
	cmd := press(a, "enter")
	if len(a.stack) != n+1 {
		t.Fatalf("la opción %q no abrió una pantalla (pila %d → %d)", id, n, len(a.stack))
	}
	return cmd
}

func assertFits(t *testing.T, a *App, w, h int, ctx string) {
	t.Helper()
	lines := strings.Split(plain(a), "\n")
	if len(lines) > h {
		t.Errorf("%s: %d filas > %d", ctx, len(lines), h)
	}
	for i, l := range lines {
		if lipgloss.Width(l) > w {
			t.Errorf("%s: la fila %d mide %d > %d: %q", ctx, i+1, lipgloss.Width(l), w, l)
		}
	}
}

func contains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("falta %q en:\n%s", w, got)
		}
	}
}

// --- menú principal ----------------------------------------------------------

func TestMenuWithoutFileLocksEverythingButOpenAndGUI(t *testing.T) {
	a, _ := newApp(t)
	s := a.stack[0].(*mainScreen)
	s.refresh(a)
	if got := s.m.current(); got != "open" {
		t.Fatalf("el cursor debería empezar en 'open', está en %q", got)
	}
	send(a, "down")
	if got := s.m.current(); got != "gui" {
		t.Errorf("sin base debe saltar las bloqueadas hasta 'gui', está en %q", got)
	}
	send(a, "down")
	if got := s.m.current(); got != "quit" {
		t.Errorf("después de 'gui' va 'quit', está en %q", got)
	}
	send(a, "down")
	if got := s.m.current(); got != "open" {
		t.Errorf("debe dar la vuelta a 'open', está en %q", got)
	}
	contains(t, plain(a), "Accesql", "Ninguna base abierta", "▸ Abrir base de datos",
		"abrí una base primero", "generá la imagen primero", "Abrir interfaz gráfica", "Cerrar")
}

func TestMenuStatesFollowTheSession(t *testing.T) {
	a, sess := newOpened(t)
	out := plain(a)
	contains(t, out, "Base: Northwind.accdb")
	if strings.Contains(out, "abrí una base primero") {
		t.Error("con una base abierta, 'Generar imagen' debe estar habilitada")
	}
	contains(t, out, "generá la imagen primero")

	if err := sess.Generate(context.Background(), session.GenerateOptions{Database: "northwind"}, nil); err != nil {
		t.Fatal(err)
	}
	out = plain(a)
	contains(t, out, "✔ Imagen «northwind» generada", "20 tablas")
	if strings.Contains(out, "primero") {
		t.Errorf("tras generar no debe quedar nada bloqueado:\n%s", out)
	}
}

func TestStatusShowsServerAndWebWhenActive(t *testing.T) {
	a, sess := newReady(t)
	port := freePort(t)
	if err := sess.StartServer(session.ServerOptions{Host: "127.0.0.1", Port: port, User: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	contains(t, plain(a), "● servidor MySQL en 127.0.0.1:"+strconv.Itoa(port))
}

func TestQuitOptionsAndCtrlC(t *testing.T) {
	isQuit := func(cmd tea.Cmd) bool { return cmd != nil && fmt.Sprint(cmd()) == fmt.Sprint(tea.Quit()) }
	a, _ := newApp(t)
	goMain(t, a, "quit")
	if !isQuit(press(a, "enter")) {
		t.Error("'Cerrar' debe salir")
	}
	b, _ := newApp(t)
	if _, cmd := b.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); !isQuit(cmd) {
		t.Error("Ctrl+C debe salir")
	}
	c, _ := newApp(t)
	if !isQuit(press(c, "q")) {
		t.Error("'q' en el menú principal debe salir")
	}
}

func TestTooSmallTerminal(t *testing.T) {
	a, _ := newApp(t)
	sized(a, 60, 20)
	contains(t, plain(a), "La terminal es muy chica", "80×24")
	sized(a, 120, 40)
	contains(t, plain(a), "Abrir base de datos")
}

// --- abrir una base ------------------------------------------------------------

func TestOpenFileFromExplorer(t *testing.T) {
	needDB(t)
	a, sess := newApp(t)
	// Una carpeta con dos bases, un .txt, una subcarpeta y un archivo oculto.
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "notas.txt"), []byte("x"), 0o644)
	b, _ := os.ReadFile(northwind)
	os.WriteFile(filepath.Join(dir, "uno.accdb"), b, 0o644)
	os.WriteFile(filepath.Join(dir, "falso.mdb"), []byte("no es una base"), 0o644)

	enter(t, a, "open")
	fs := a.top().(*fileScreen)
	fs.load(dir)
	out := plain(a)
	contains(t, out, "uno.accdb", "falso.mdb", "sub/", "(subir una carpeta)")
	if strings.Contains(out, "notas.txt") {
		t.Error("solo deben verse carpetas y bases de Access")
	}

	// Un archivo corrupto se rechaza con un mensaje y no queda abierto.
	for fs.m.current() != "e1" && fs.m.current() != "e0" && fs.m.current() != "e2" {
		send(a, "down")
	}
	for i := 0; i < 10 && !strings.Contains(fs.m.items[fs.m.cur].label, "falso"); i++ {
		send(a, "down")
	}
	send(a, "enter")
	if len(a.stack) != 2 || sess.State().File != "" || !strings.Contains(plain(a), "✖") {
		t.Errorf("un archivo corrupto no debe abrirse (pila=%d, archivo=%q)\n%s", len(a.stack), sess.State().File, plain(a))
	}

	// Una base válida se abre, vuelve al menú y avisa.
	for i := 0; i < 10 && !strings.Contains(fs.m.items[fs.m.cur].label, "uno"); i++ {
		send(a, "down")
	}
	send(a, "enter")
	if len(a.stack) != 1 || !strings.HasSuffix(sess.State().File, "uno.accdb") {
		t.Fatalf("no se abrió la base (pila=%d, archivo=%q)", len(a.stack), sess.State().File)
	}
	contains(t, plain(a), "Base abierta: uno.accdb", "Base: uno.accdb")
}

func TestExplorerNavigatesUpAndKeepsListingOnError(t *testing.T) {
	a, _ := newApp(t)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "hija"), 0o755)
	enter(t, a, "open")
	fs := a.top().(*fileScreen)
	fs.load(filepath.Join(dir, "hija"))
	send(a, "backspace")
	if fs.l.Path != dir {
		t.Errorf("Retroceso debe subir una carpeta: %q", fs.l.Path)
	}
	fs.load(filepath.Join(dir, "no-existe"))
	if fs.err == "" || fs.l.Path != dir {
		t.Errorf("una carpeta inexistente debe mostrar el error y conservar el listado (err=%q path=%q)", fs.err, fs.l.Path)
	}
}

// --- generar la imagen ------------------------------------------------------

func TestGenerateFlow(t *testing.T) {
	a, sess := newOpened(t)
	cmd := enter(t, a, "gen")
	if cmd == nil {
		t.Fatal("la pantalla debe darle el foco al nombre")
	}
	contains(t, plain(a), "Nombre de la base", "northwind", "Generar imagen")

	send(a, "enter") // del nombre al botón
	cmd = press(a, "enter")
	if !sess.State().Generating && cmd == nil {
		t.Fatal("Enter en 'Generar imagen' debe lanzar la generación")
	}
	sawProgress := false
	settle(t, a, cmd, func() bool {
		if p := sess.State().Progress; p != nil && strings.Contains(plain(a), fmt.Sprintf("Tabla %d de 20", p.Number)) {
			sawProgress = true
		}
		return len(a.stack) == 1 && !sess.State().Generating
	})
	_ = sawProgress // con Northwind termina en una fracción de segundo: el progreso se prueba aparte
	st := sess.State()
	if !st.ImageReady || st.Database != "northwind" || st.Tables != 20 {
		t.Fatalf("estado tras generar: %+v", st)
	}
	contains(t, plain(a), "✔ ¡Imagen MySQL generada!", "20 tablas")
}

func TestGenerateRejectsBadName(t *testing.T) {
	a, sess := newOpened(t)
	enter(t, a, "gen")
	gs := a.top().(*genScreen)
	gs.name.SetValue("mala base")
	send(a, "down", "enter")
	if sess.State().Generating || gs.running {
		t.Fatal("con un nombre inválido no debe generar")
	}
	contains(t, plain(a), "El nombre solo puede tener letras")
}

func TestGenerateScreenShowsProgressAndCancels(t *testing.T) {
	a, sess := newOpened(t)
	enter(t, a, "gen")
	gs := a.top().(*genScreen)
	gs.running = true // como si ya hubiera empezado
	contains(t, plain(a), "Abriendo el archivo")

	if cmd := press(a, "esc"); cmd != nil || !gs.canceling {
		t.Error("Esc durante la generación debe pedir cancelar")
	}
	contains(t, plain(a), "Cancelando")
	_ = sess

	// El fin de una generación cancelada vuelve al menú con un aviso.
	a.Update(genDone{id: gs.id, err: context.Canceled})
	if len(a.stack) != 1 {
		t.Fatalf("debe volver al menú (pila=%d)", len(a.stack))
	}
	contains(t, plain(a), "Generación cancelada.")

	// Una respuesta de otra pantalla se ignora.
	enter(t, a, "gen")
	n := len(a.stack)
	a.Update(genDone{id: -1})
	if len(a.stack) != n {
		t.Error("una respuesta ajena no debe cerrar la pantalla")
	}
}

func TestGenerateFailureIsShown(t *testing.T) {
	a, _ := newOpened(t)
	enter(t, a, "gen")
	gs := a.top().(*genScreen)
	a.Update(genDone{id: gs.id, err: errors.New("archivo corrupto")})
	contains(t, plain(a), "No se pudo generar: archivo corrupto")
}

// --- servidor MySQL --------------------------------------------------------

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestServerScreenStartStop(t *testing.T) {
	a, sess := newReady(t)
	enter(t, a, "server")
	ss := a.top().(*serverScreen)

	// Sin marcar la casilla no se puede iniciar; al marcarla se habilitan los campos.
	contains(t, plain(a), "marcá la casilla primero")
	send(a, "space")
	if !ss.on {
		t.Fatal("Espacio debe marcar la casilla")
	}

	// Ir a "Iniciar servidor" sin contraseña: error.
	ss.in[1].SetValue(strconv.Itoa(freePort(t)))
	for ss.focus != rowAction {
		send(a, "down")
	}
	send(a, "enter")
	contains(t, plain(a), "La contraseña es obligatoria")
	if sess.State().Server.Running {
		t.Fatal("sin contraseña no debe iniciar")
	}

	// Con contraseña, inicia.
	ss.in[3].SetValue("secreta")
	send(a, "enter")
	st := sess.State().Server
	if !st.Running || st.User != "access" || st.Database != "northwind" {
		t.Fatalf("estado del servidor: %+v", st)
	}
	out := plain(a)
	contains(t, out, "● Activo", "mysql -h 127.0.0.1 -P "+ss.in[1].Value()+" -u access -p northwind",
		"Detener servidor", "Base         northwind")
	if strings.Contains(out, "secreta") {
		t.Error("la contraseña no debe mostrarse")
	}
	contains(t, plain(a), "Servidor iniciado.")

	// Detener.
	send(a, "enter")
	if sess.State().Server.Running {
		t.Error("Enter sobre 'Detener servidor' debe detenerlo")
	}
	contains(t, plain(a), "Servidor detenido.")
}

func TestServerScreenShowsAnAlreadyRunningServer(t *testing.T) {
	a, sess := newReady(t)
	port := freePort(t)
	if err := sess.StartServer(session.ServerOptions{Host: "127.0.0.1", Port: port, User: "mio", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	enter(t, a, "server")
	out := plain(a)
	contains(t, out, "[x] Exponer", "Detener servidor", strconv.Itoa(port), "mio")
}

func TestServerScreenReportsPortInUse(t *testing.T) {
	a, _ := newReady(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	enter(t, a, "server")
	ss := a.top().(*serverScreen)
	send(a, "space")
	ss.in[1].SetValue(strconv.Itoa(l.Addr().(*net.TCPAddr).Port))
	ss.in[3].SetValue("p")
	for ss.focus != rowAction {
		send(a, "down")
	}
	send(a, "enter")
	contains(t, plain(a), "el puerto ya está", "en uso por otro programa")
}

// --- consultas ----------------------------------------------------------------

func TestQueryFlow(t *testing.T) {
	a, _ := newReady(t)
	enter(t, a, "query")
	qs := a.top().(*queryScreen)

	send(a, "ctrl+r")
	contains(t, plain(a), "Escribí una consulta primero.")

	typeText(a, "SELECT ID, Company FROM Customers ORDER BY ID")
	cmd := press(a, "ctrl+r")
	settle(t, a, cmd, func() bool { return !qs.running && qs.res != nil })
	out := plain(a)
	contains(t, out, "✔", "29 filas", "Company A", "Company E")
	if len(qs.res.Rows) != 29 || qs.res.Columns[1] != "Company" {
		t.Fatalf("resultado: %d filas, columnas %v", len(qs.res.Rows), qs.res.Columns)
	}

}

func TestQueryReplacesThePreviousResultAndShowsErrors(t *testing.T) {
	a, _ := newReady(t)
	enter(t, a, "query")
	qs := a.top().(*queryScreen)
	run := func(sql string) {
		qs.ed.Reset()
		qs.setEditorFocus(true)
		typeText(a, sql)
		settle(t, a, press(a, "ctrl+r"), func() bool { return !qs.running })
	}

	run("SELECT ID, Company FROM Customers LIMIT 3")
	contains(t, plain(a), "3 filas", "Company")
	run("SELECT COUNT(*) AS cantidad FROM Orders")
	out := plain(a)
	contains(t, out, "1 filas", "cantidad")
	if strings.Contains(out, "Company A") {
		t.Errorf("el resultado anterior no debe seguir en pantalla:\n%s", out)
	}

	run("SELEC roto")
	contains(t, plain(a), "✖", "syntax error")
	run("DELETE FROM Customers")
	contains(t, plain(a), "✖", "read only")
	run("SELECT COUNT(*) AS n FROM Customers")
	contains(t, plain(a), "✔")
	if qs.res.Rows[0][0] != int64(29) {
		t.Errorf("tras los errores el motor debe seguir funcionando: %v", qs.res.Rows[0][0])
	}
}

func TestQueryGridScrollsAndShowsTruncation(t *testing.T) {
	a, _ := newReady(t)
	enter(t, a, "query")
	qs := a.top().(*queryScreen)
	typeText(a, "SELECT * FROM Customers")
	settle(t, a, press(a, "ctrl+r"), func() bool { return !qs.running && qs.res != nil })

	send(a, "tab", "tab") // editor → botones → grilla
	if qs.focus != focusGrid {
		t.Fatalf("Tab dos veces debe llegar a la grilla (foco=%d)", qs.focus)
	}
	send(a, "down", "down", "down", "right", "right")
	if qs.g.cur != 3 || qs.g.colOff != 2 {
		t.Errorf("cursor=%d colOff=%d", qs.g.cur, qs.g.colOff)
	}
	send(a, "end")
	if qs.g.cur != 28 {
		t.Errorf("End debe ir a la última fila: %d", qs.g.cur)
	}
	send(a, "home")
	if qs.g.cur != 0 {
		t.Errorf("Home debe ir a la primera fila: %d", qs.g.cur)
	}
	assertFits(t, a, 80, 24, "grilla desplazada")

	// Con más filas que el tope se avisa.
	qs.setEditorFocus(true)
	qs.ed.Reset()
	typeText(a, "SELECT 1 AS n FROM Customers c1 CROSS JOIN Orders o1 CROSS JOIN Products p1 LIMIT 1500")
	settle(t, a, press(a, "ctrl+r"), func() bool { return !qs.running && qs.truncated })
	contains(t, plain(a), "hay más: se muestran las primeras 1.000")
}

func TestQueryTablePickerWritesTheQuery(t *testing.T) {
	a, _ := newReady(t)
	enter(t, a, "query")
	qs := a.top().(*queryScreen)
	send(a, "tab")   // botones
	send(a, "right") // Tablas
	send(a, "enter") // abre el selector
	if _, ok := a.top().(*tablePicker); !ok {
		t.Fatalf("debería abrir el selector de tablas, hay %T", a.top())
	}
	contains(t, plain(a), "Customers", "29 filas")
	send(a, "enter") // la primera tabla
	if len(a.stack) != 2 || qs.focus != focusEditor {
		t.Fatalf("debe volver al editor (pila=%d foco=%d)", len(a.stack), qs.focus)
	}
	if got := qs.ed.Value(); got != "SELECT * FROM `Customers` LIMIT 100" {
		t.Errorf("el editor tiene %q", got)
	}
}

func TestQueryExportsToFiles(t *testing.T) {
	a, sess := newReady(t)
	dir := filepath.Dir(sess.State().File)
	enter(t, a, "query")
	qs := a.top().(*queryScreen)

	// Sin resultado no hay nada que exportar.
	qs.focus = focusButtons
	qs.btn = 3 // JSON
	send(a, "enter")
	contains(t, plain(a), "Primero ejecutá una consulta.")

	qs.setEditorFocus(true)
	typeText(a, "SELECT ID, Company FROM Customers ORDER BY ID LIMIT 3")
	settle(t, a, press(a, "ctrl+r"), func() bool { return !qs.running && qs.res != nil })

	for _, c := range []struct {
		btn  int
		ext  string
		want string
	}{{3, ".json", `"Company": "Company A"`}, {4, ".csv", "ID;Company"}, {5, ".sql", "INSERT INTO `resultado`"}} {
		qs.focus, qs.btn = focusButtons, c.btn
		before := a.flash
		cmd := press(a, "enter")
		settle(t, a, cmd, func() bool {
			return strings.HasPrefix(a.flash, "Guardado: ") && a.flash != before || strings.Contains(a.flash, "No se pudo")
		})
		files, _ := filepath.Glob(filepath.Join(dir, "resultado-*"+c.ext))
		if len(files) == 0 {
			t.Fatalf("no se creó el archivo %s (aviso: %q)", c.ext, a.flash)
		}
		b, _ := os.ReadFile(files[0])
		if !strings.Contains(string(b), c.want) {
			t.Errorf("%s no contiene %q:\n%s", c.ext, c.want, b)
		}
		os.Remove(files[0])
		time.Sleep(1100 * time.Millisecond) // el nombre lleva la hora al segundo
	}
}

func TestQueryClearAndBack(t *testing.T) {
	a, _ := newReady(t)
	enter(t, a, "query")
	qs := a.top().(*queryScreen)
	typeText(a, "SELECT 1 AS x")
	settle(t, a, press(a, "ctrl+r"), func() bool { return !qs.running && qs.res != nil })
	qs.focus, qs.btn = focusButtons, 2 // Limpiar
	send(a, "enter")
	if qs.res != nil || qs.ed.Value() != "" || qs.focus != focusEditor {
		t.Error("'Limpiar' debe borrar el editor y el resultado y volver al editor")
	}
	qs.focus, qs.btn = focusButtons, len(queryButtons)-1
	send(a, "enter")
	if len(a.stack) != 1 {
		t.Error("'Volver' debe volver al menú")
	}
}

// --- convertir a MySQL ----------------------------------------------------------

func TestConvertNewShowsAndSavesTheScript(t *testing.T) {
	a, sess := newReady(t)
	enter(t, a, "convert")
	send(a, "enter") // "Crear una base de datos nueva"
	cn, ok := a.top().(*convertNew)
	if !ok {
		t.Fatalf("se esperaba convertNew, hay %T", a.top())
	}
	contains(t, plain(a), "northwind", "[x] Incluir los datos", "[x] Incluir las relaciones", "[ ] Borrar la base")

	for cn.focus != 4 {
		send(a, "down")
	}
	cmd := press(a, "enter")
	settle(t, a, cmd, func() bool { _, isScript := a.top().(*scriptScreen); return isScript })
	ss := a.top().(*scriptScreen)
	out := plain(a)
	contains(t, out, "-- Accesql", "Northwind.accdb → MySQL", "Guardar .sql")
	if len(ss.lines) < 100 {
		t.Errorf("el script de Northwind debería tener cientos de líneas, tiene %d", len(ss.lines))
	}

	// Desplazarse.
	send(a, "pgdown")
	if ss.off == 0 {
		t.Error("PgDn debe desplazar el script")
	}
	assertFits(t, a, 80, 24, "script desplazado")

	// Guardar el script completo junto a la base.
	ss.btn = 0
	cmd = press(a, "enter")
	settle(t, a, cmd, func() bool { return strings.HasPrefix(a.flash, "Guardado: ") })
	b, err := os.ReadFile(filepath.Join(filepath.Dir(sess.State().File), "northwind.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "CREATE DATABASE `northwind`") || !strings.Contains(string(b), "FOREIGN KEY") ||
		!strings.Contains(string(b), "INSERT INTO `Customers`") || len(b) < 20000 {
		t.Errorf("el archivo guardado debe ser el script completo (%d bytes)", len(b))
	}
}

func TestConvertNewValidatesName(t *testing.T) {
	a, _ := newReady(t)
	enter(t, a, "convert")
	send(a, "enter")
	cn := a.top().(*convertNew)
	cn.name.SetValue("mala base!")
	for cn.focus != 4 {
		send(a, "down")
	}
	if cmd := press(a, "enter"); cmd != nil {
		t.Error("con un nombre inválido no debe generar")
	}
	contains(t, plain(a), "El nombre solo puede tener letras")
}

func TestConvertAddRequiresSelectionAndAppliesOptions(t *testing.T) {
	a, _ := newReady(t)
	enter(t, a, "convert")
	send(a, "down", "enter") // "Agregar datos a tablas que ya existen"
	ca, ok := a.top().(*convertAdd)
	if !ok {
		t.Fatalf("se esperaba convertAdd, hay %T", a.top())
	}
	contains(t, plain(a), "20 de 20 tablas marcadas")

	send(a, "n")
	for ca.cur != ca.rowGen() {
		send(a, "down")
	}
	if cmd := press(a, "enter"); cmd != nil {
		t.Fatal("sin tablas marcadas no debe generar")
	}
	contains(t, plain(a), "Marcá al menos una tabla")

	// Marcar solo Customers, renombrarla y limitar las columnas.
	send(a, "a")
	for i := range ca.sel {
		ca.sel[i] = ca.tables[i].Name == "Customers"
	}
	ca.cur, ca.top = 0, 0
	for ca.tables[ca.cur].Name != "Customers" {
		send(a, "down")
	}
	send(a, "e")
	for i := 0; i < len("Customers"); i++ {
		send(a, "backspace")
	}
	typeText(a, "CLIENTE")
	send(a, "enter")
	send(a, "c")
	typeText(a, "id, company, extra")
	send(a, "enter")
	if ca.dst[ca.cur] != "CLIENTE" || ca.cols[ca.cur] != "id, company, extra" {
		t.Fatalf("destino=%q columnas=%q", ca.dst[ca.cur], ca.cols[ca.cur])
	}
	contains(t, plain(a), "→ CLIENTE  (3 col.)")

	for ca.cur != ca.rowDup() {
		send(a, "down")
	}
	send(a, "right") // Actualizarlo
	for ca.cur != ca.rowGen() {
		send(a, "down")
	}
	cmd := press(a, "enter")
	settle(t, a, cmd, func() bool { _, isScript := a.top().(*scriptScreen); return isScript })
	ss := a.top().(*scriptScreen)
	text := strings.Join(ss.lines, "\n")
	contains(t, text, "INSERT INTO `CLIENTE` (`ID`, `Company`) VALUES", "ON DUPLICATE KEY UPDATE `Company` = VALUES(`Company`);", "START TRANSACTION;", "COMMIT;")
}

// --- diagrama -----------------------------------------------------------------

func TestDiagramShowsRealRelations(t *testing.T) {
	a, _ := newReady(t)
	cmd := enter(t, a, "diagram")
	contains(t, plain(a), "Leyendo la estructura")
	ds := a.top().(*diagramScreen)
	settle(t, a, cmd, func() bool { return ds.sc != nil || ds.err != "" })
	if ds.err != "" {
		t.Fatal(ds.err)
	}
	out := plain(a)
	contains(t, out, "[+] Customers", "[+] Orders", "relaciones de Access")

	// Desplegar Orders: columnas con PK/FK y las relaciones reales.
	for ds.sc.Tables[ds.cur].Name != "Orders" {
		send(a, "down")
	}
	send(a, "enter")
	out = plain(a)
	contains(t, out, "[-] Orders", "FK Customer ID", "→ Customers (Customer ID → ID)", "← Order Details")
	if !strings.Contains(out, "columnas más") {
		t.Error("una tabla con muchas columnas debe mostrar las primeras y avisar cuántas faltan")
	}
	send(a, "c") // ver todas las columnas
	contains(t, plain(a), "[-] Orders")
	send(a, "pgdown")
	contains(t, plain(a), "← Order Details")
	send(a, "c", "up", "down") // vuelve a seguir al cursor
	contains(t, plain(a), "[-] Orders")
	send(a, "left")
	if strings.Contains(plain(a), "FK Customer ID") {
		t.Error("← debe cerrar la tabla")
	}

	// Abrir todas: se puede recorrer sin desbordar.
	send(a, "+")
	for i := 0; i < len(ds.sc.Tables)+1; i++ {
		assertFits(t, a, 80, 24, "diagrama con todo abierto")
		send(a, "down")
	}
	send(a, "-")
	if strings.Contains(plain(a), "[-]") {
		t.Error("'-' debe cerrar todas")
	}
}

// --- interfaz gráfica compartida --------------------------------------------------

func TestGUIScreenSharesTheSession(t *testing.T) {
	a, sess := newReady(t)
	enter(t, a, "gui")
	if !a.webRunning() {
		t.Fatal("la pantalla debe levantar la interfaz web")
	}
	out := plain(a)
	contains(t, out, "La interfaz web está activa", "http://127.0.0.1:", "#t=", "Detener la interfaz gráfica", "Se abrió en tu navegador")

	base, token, _ := strings.Cut(a.web.URL, "/#t=")
	req, _ := http.NewRequest("GET", base+"/api/state", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		ImageReady bool   `json:"imageReady"`
		Database   string `json:"database"`
		File       string `json:"file"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if !st.ImageReady || st.Database != "northwind" || st.File != sess.State().File {
		t.Errorf("la web debe ver la MISMA base ya cargada: %+v", st)
	}
	contains(t, plain(a), "Se abrió en tu navegador")

	// Volver y entrar de nuevo no levanta otro servidor.
	url := a.web.URL
	send(a, "esc")
	enter(t, a, "gui")
	if a.web.URL != url {
		t.Error("debe reutilizar la interfaz ya activa")
	}
	contains(t, plain(a), "Abrir de nuevo en el navegador")

	// Detener.
	send(a, "down", "enter")
	if a.webRunning() || len(a.stack) != 1 {
		t.Errorf("'Detener' debe apagar la web y volver (web=%v pila=%d)", a.webRunning(), len(a.stack))
	}
	contains(t, plain(a), "Interfaz gráfica detenida.")
	if _, err := http.Get(base + "/"); err == nil {
		t.Error("tras detenerla, el puerto debe estar cerrado")
	}
}

func TestGUIScreenCloseStopsTheWeb(t *testing.T) {
	a, _ := newReady(t)
	enter(t, a, "gui")
	base, _, _ := strings.Cut(a.web.URL, "/#t=")
	a.Close()
	if _, err := http.Get(base + "/"); err == nil {
		t.Error("Close debe apagar la interfaz web que levantó el menú")
	}
}

// --- volver y límites de pantalla ------------------------------------------------

func TestEveryScreenGoesBackWithEsc(t *testing.T) {
	for _, id := range []string{"open", "gen", "server", "query", "convert", "diagram", "gui"} {
		a, _ := newReady(t)
		enter(t, a, id)
		send(a, "esc")
		if id == "query" {
			send(a, "esc") // la primera Esc pasa del editor a los botones
		}
		if len(a.stack) != 1 {
			t.Errorf("%s: Esc no volvió al menú (pila=%d)", id, len(a.stack))
		}
	}
}

func TestNoScreenOverflows(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {100, 30}, {200, 50}} {
		w, h := sz[0], sz[1]
		a, sess := newReady(t)
		sized(a, w, h)
		assertFits(t, a, w, h, fmt.Sprintf("menú %dx%d", w, h))

		open := func(id string) tea.Cmd { return enter(t, a, id) }
		back := func() {
			for len(a.stack) > 1 {
				send(a, "esc")
			}
		}
		check := func(name string) { assertFits(t, a, w, h, fmt.Sprintf("%s %dx%d", name, w, h)) }

		open("open")
		check("explorador")
		back()

		open("gen")
		check("generar")
		back()

		open("server")
		ss := a.top().(*serverScreen)
		send(a, "space")
		for ss.focus != rowAction {
			send(a, "down")
		}
		send(a, "enter")
		check("servidor con error")
		ss.in[1].SetValue(strconv.Itoa(freePort(t)))
		ss.in[3].SetValue("p")
		send(a, "enter")
		check("servidor activo")
		sess.StopServer()
		back()

		cmd := open("query")
		_ = cmd
		qs := a.top().(*queryScreen)
		typeText(a, "SELECT * FROM Customers")
		settle(t, a, press(a, "ctrl+r"), func() bool { return !qs.running && qs.res != nil })
		check("consulta con resultado")
		send(a, "tab", "tab", "right", "right", "right")
		check("grilla desplazada")
		back()

		open("convert")
		send(a, "enter")
		check("base nueva")
		send(a, "esc")
		send(a, "down", "enter")
		ca := a.top().(*convertAdd)
		for ca.cur != ca.rowGen() {
			check("agregar")
			send(a, "down")
		}
		check("agregar al final")
		back()

		cmd = open("diagram")
		ds := a.top().(*diagramScreen)
		settle(t, a, cmd, func() bool { return ds.sc != nil })
		send(a, "+")
		for i := 0; i < len(ds.sc.Tables)+1; i++ {
			check("diagrama")
			send(a, "down")
		}
		back()

		open("gui")
		check("interfaz gráfica")
		back()
	}
}

// Cada cuerpo debe entrar en el espacio disponible sin que se lo recorten
// (fitLines recorta en silencio, así que se mide antes).
func TestBodiesFitWithoutTruncation(t *testing.T) {
	a, _ := newReady(t)
	check := func(name string) {
		t.Helper()
		body := a.top().view(a, min(a.w-2, maxCW))
		if n := strings.Count(body, "\n") + 1; n > bodyH {
			t.Errorf("%s: el cuerpo tiene %d filas y solo caben %d", name, n, bodyH)
		}
	}
	for _, id := range []string{"open", "gen", "server", "query", "convert", "gui"} {
		enter(t, a, id)
		check(id)
		for len(a.stack) > 1 {
			send(a, "esc")
		}
	}
	cmd := enter(t, a, "diagram")
	ds := a.top().(*diagramScreen)
	settle(t, a, cmd, func() bool { return ds.sc != nil })
	send(a, "+")
	check("diagrama abierto")
}

func TestHelpers(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1.000", 511: "511", 1234567: "1.234.567"} {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%d) = %q, quiero %q", in, got, want)
		}
	}
	if got := fmtSize(3_800_000); got != "3.6 MB" {
		t.Errorf("fmtSize = %q", got)
	}
	if got := splitColumns(" id, Nombre ;\nemail ,, "); strings.Join(got, "|") != "id|Nombre|email" {
		t.Errorf("splitColumns = %v", got)
	}
	if got := cellText("a\nb\tc"); got != "a b c" {
		t.Errorf("cellText = %q", got)
	}
}
