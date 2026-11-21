package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// --- ayudas ----------------------------------------------------------------

var keyTypes = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "up": tea.KeyUp, "down": tea.KeyDown,
	"left": tea.KeyLeft, "right": tea.KeyRight, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
	"backspace": tea.KeyBackspace, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"ctrl+r": tea.KeyCtrlR, "ctrl+c": tea.KeyCtrlC,
}

func send(a *App, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch t, ok := keyTypes[k]; {
		case ok:
			msg = tea.KeyMsg{Type: t}
		case k == "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		a.Update(msg)
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

func sized(w, h int) *App {
	a := New()
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

func plain(a *App) string { return ansi.Strip(a.View()) }

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

// ready abre una base y genera la imagen (avanzando los "tics" a mano).
func ready(t *testing.T, a *App) {
	t.Helper()
	goMain(t, a, "open")
	send(a, "enter", "enter") // abre el explorador y elige el primer archivo
	if a.st.filePath == "" {
		t.Fatal("no se eligió archivo")
	}
	goMain(t, a, "gen")
	send(a, "enter")
	g, ok := a.top().(*genScreen)
	if !ok {
		t.Fatalf("se esperaba la pantalla de generación, hay %T", a.top())
	}
	for i := 0; i < len(mockTables); i++ {
		a.Update(genTick{id: g.id})
	}
	if !a.st.imageReady || len(a.stack) != 1 {
		t.Fatalf("la imagen no quedó lista (ready=%v, pila=%d)", a.st.imageReady, len(a.stack))
	}
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

// --- pruebas ---------------------------------------------------------------

func TestMenuSkipsLockedOptions(t *testing.T) {
	a := sized(80, 24)
	s := a.stack[0].(*mainScreen)
	if got := s.m.current(); got != "open" {
		t.Fatalf("el cursor debería empezar en 'open', está en %q", got)
	}
	send(a, "down")
	if got := s.m.current(); got != "gui" {
		t.Errorf("sin base abierta debe saltar las bloqueadas hasta 'gui', está en %q", got)
	}
	send(a, "down")
	if got := s.m.current(); got != "quit" {
		t.Errorf("después de 'gui' va 'quit', está en %q", got)
	}
	send(a, "down")
	if got := s.m.current(); got != "open" {
		t.Errorf("debe dar la vuelta a 'open', está en %q", got)
	}
}

func TestMenuShowsSelectionAndLockedHints(t *testing.T) {
	a := sized(80, 24)
	out := plain(a)
	for _, want := range []string{"Accesql", "v" + Version, "Ninguna base abierta", "▸ Abrir base de datos",
		"abrí una base primero", "generá la imagen primero", "Abrir interfaz gráfica", "Cerrar"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en el menú:\n%s", want, out)
		}
	}
}

func TestFullFlowUnlocksOptions(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	out := plain(a)
	for _, want := range []string{"✔ ¡Imagen MySQL generada!", "20 tablas", "508 filas"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q tras generar:\n%s", want, out)
		}
	}
	if strings.Contains(out, "generá la imagen primero") {
		t.Error("tras generar no debería quedar nada bloqueado")
	}
}

func TestOpeningAnotherFileLocksAgain(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	goMain(t, a, "open")
	send(a, "enter", "enter")
	if a.st.imageReady {
		t.Error("al abrir otra base hay que volver a generar la imagen")
	}
}

func TestGenerateCanBeCancelled(t *testing.T) {
	a := sized(80, 24)
	goMain(t, a, "open")
	send(a, "enter", "enter")
	goMain(t, a, "gen")
	send(a, "enter", "esc")
	if a.st.imageReady || len(a.stack) != 1 {
		t.Errorf("cancelar no debe dejar la imagen lista (ready=%v pila=%d)", a.st.imageReady, len(a.stack))
	}
}

func TestEveryScreenGoesBackWithEscAndVolver(t *testing.T) {
	for _, id := range []string{"open", "server", "query", "convert", "diagram", "gui"} {
		for _, how := range []string{"esc", "volver"} {
			a := sized(80, 24)
			ready(t, a)
			goMain(t, a, id)
			send(a, "enter")
			if len(a.stack) != 2 {
				t.Fatalf("%s: no se abrió la pantalla", id)
			}
			switch how {
			case "esc":
				send(a, "esc")
				if id == "query" {
					send(a, "esc") // la primera Esc pasa del editor a los botones
				}
			default:
				switch scr := a.top().(type) {
				case *fileScreen:
					for i := 0; i < 40 && scr.m.current() != "back"; i++ {
						send(a, "down")
					}
					send(a, "enter")
				case *queryScreen:
					send(a, "esc", "left", "enter") // botones: de "Ejecutar" a "Volver" (da la vuelta)
				default:
					send(a, "up", "enter") // "Volver" es la última opción: ↑ desde el inicio da la vuelta
				}
			}
			if len(a.stack) != 1 {
				t.Errorf("%s (%s): no volvió al menú (pila=%d)", id, how, len(a.stack))
			}
		}
	}
}

func TestQuitOptionsAndCtrlC(t *testing.T) {
	a := sized(80, 24)
	goMain(t, a, "quit")
	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || fmt.Sprint(cmd()) != fmt.Sprint(tea.Quit()) {
		t.Error("'Cerrar' debe salir")
	}
	if _, cmd := sized(80, 24).Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil || fmt.Sprint(cmd()) != fmt.Sprint(tea.Quit()) {
		t.Error("Ctrl+C debe salir")
	}
	if _, cmd := sized(80, 24).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Error("'q' en el menú principal debe salir")
	}
}

func TestTooSmallTerminal(t *testing.T) {
	a := sized(60, 20)
	out := plain(a)
	if !strings.Contains(out, "La terminal es muy chica") || !strings.Contains(out, "80×24") {
		t.Errorf("debería pedir agrandar la terminal:\n%s", out)
	}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !strings.Contains(plain(a), "Abrir base de datos") {
		t.Error("al agrandar debe mostrar el menú")
	}
}

func TestServerValidationAndStartStop(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	goMain(t, a, "server")
	send(a, "enter") // entra
	send(a, "space") // marca la casilla

	// Ir a "Iniciar servidor" (fila 6): casilla → 5 campos → acción.
	for i := 0; i < 6; i++ {
		send(a, "down")
	}
	send(a, "enter")
	if out := plain(a); !strings.Contains(out, "La contraseña es obligatoria") {
		t.Errorf("sin contraseña debe avisar:\n%s", out)
	}

	// Escribir la contraseña (campo 4 → fila 4).
	for i := 0; i < 2; i++ {
		send(a, "up")
	}
	typeText(a, "secreta")
	send(a, "down", "down", "enter")
	if a.st.serverAddr != "127.0.0.1:3306" {
		t.Fatalf("el servidor debería estar activo, serverAddr=%q", a.st.serverAddr)
	}
	out := plain(a)
	for _, want := range []string{"● Activo", "mysql -h 127.0.0.1 -P 3306 -u access -p access", "Detener servidor"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q con el servidor activo:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secreta") {
		t.Error("la contraseña no debe mostrarse en pantalla")
	}

	send(a, "enter") // detener
	if a.st.serverAddr != "" {
		t.Error("al detener, serverAddr debe quedar vacío")
	}
}

func TestQueryReplacesResultAndClears(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	goMain(t, a, "query")
	send(a, "enter")

	send(a, "ctrl+r")
	if !strings.Contains(plain(a), "Escribí una consulta primero.") {
		t.Error("ejecutar sin texto debe avisar")
	}
	typeText(a, "SELECT 1")
	send(a, "ctrl+r")
	out := plain(a)
	for _, want := range []string{"5 filas", "Company A", "Gratacos Solsona", "NULL", "Credit Limit"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en el resultado:\n%s", want, out)
		}
	}
	q := a.top().(*queryScreen)
	first := q.ranAt
	typeText(a, "x")
	send(a, "ctrl+r")
	if !q.ranAt.After(first) && q.ranText != "SELECT 1x" {
		t.Error("cada ejecución debe reemplazar el resultado anterior")
	}

	// Limpiar: Esc → botones, ← ← → … "Limpiar" es el segundo botón.
	send(a, "esc", "right", "enter")
	if q.ran || strings.TrimSpace(q.ed.Value()) != "" {
		t.Error("'Limpiar' debe borrar el editor y el resultado")
	}
}

func TestConvertAddRequiresSelection(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	goMain(t, a, "convert")
	send(a, "enter", "down", "enter") // "Agregar datos a tablas que ya existen"
	if _, ok := a.top().(*convertAdd); !ok {
		t.Fatalf("se esperaba convertAdd, hay %T", a.top())
	}
	send(a, "n") // ninguna
	c := a.top().(*convertAdd)
	for c.cur != c.rowGen() {
		send(a, "down")
	}
	send(a, "enter")
	if !strings.Contains(plain(a), "Marcá al menos una tabla") {
		t.Error("sin tablas marcadas debe avisar")
	}
	send(a, "a", "enter")
	if _, ok := a.top().(*scriptScreen); !ok {
		t.Fatalf("con tablas marcadas debe mostrar el script, hay %T", a.top())
	}
	if out := plain(a); !strings.Contains(out, "INSERT IGNORE INTO") || !strings.Contains(out, "START TRANSACTION;") {
		t.Errorf("el script debería traer INSERT IGNORE y la transacción:\n%s", out)
	}
}

func TestConvertAddRenameAndDuplicateMode(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	goMain(t, a, "convert")
	send(a, "enter", "down", "enter")
	send(a, "e")
	for i := 0; i < len("Customers"); i++ {
		send(a, "backspace")
	}
	typeText(a, "CLIENTE")
	send(a, "enter")
	c := a.top().(*convertAdd)
	if c.dst[0] != "CLIENTE" {
		t.Fatalf("el destino debería ser CLIENTE, es %q", c.dst[0])
	}
	for c.cur != c.rowDup() {
		send(a, "down")
	}
	send(a, "right") // Actualizarlo
	for c.cur != c.rowGen() {
		send(a, "down")
	}
	send(a, "enter")
	out := plain(a)
	if !strings.Contains(out, "`CLIENTE`") || !strings.Contains(out, "ON DUPLICATE KEY UPDATE") {
		t.Errorf("el script debería usar CLIENTE y ON DUPLICATE KEY UPDATE:\n%s", out)
	}
}

func TestConvertNewValidatesName(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	goMain(t, a, "convert")
	send(a, "enter", "enter")
	c := a.top().(*convertNew)
	for i := 0; i < 40; i++ {
		send(a, "backspace")
	}
	typeText(a, "mala base!")
	for c.focus != 3 {
		send(a, "down")
	}
	send(a, "enter")
	if !strings.Contains(plain(a), "solo puede tener letras") {
		t.Error("un nombre inválido debe avisar")
	}
}

func TestDiagramExpandCollapse(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	goMain(t, a, "diagram")
	send(a, "enter")
	if !strings.Contains(plain(a), "Customer ID") {
		t.Fatal("'Orders' viene desplegada: debería verse 'Customer ID'")
	}
	d := a.top().(*diagramScreen)
	d.cur = 0
	send(a, "right")
	if !strings.Contains(plain(a), "Company") {
		t.Error("→ debe desplegar la tabla")
	}
	send(a, "left")
	if strings.Contains(plain(a), "├─ ") && strings.Contains(plain(a), "Last Name") {
		t.Error("← debe cerrar la tabla")
	}
}

// Ninguna pantalla puede desbordar la ventana, ni a 80×24 ni más grande.
func TestNoScreenOverflows(t *testing.T) {
	sizes := [][2]int{{80, 24}, {100, 30}, {200, 50}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		a := sized(w, h)
		assertFits(t, a, w, h, fmt.Sprintf("menú %dx%d", w, h))
		ready(t, a)
		assertFits(t, a, w, h, fmt.Sprintf("menú listo %dx%d", w, h))

		for _, id := range []string{"open", "server", "query", "convert", "diagram", "gui"} {
			goMain(t, a, id)
			send(a, "enter")
			assertFits(t, a, w, h, fmt.Sprintf("%s %dx%d", id, w, h))

			switch id {
			case "server":
				send(a, "space")
				typeText(a, "")
				for i := 0; i < 6; i++ {
					send(a, "down")
				}
				send(a, "enter") // error de validación
				assertFits(t, a, w, h, "server error")
				send(a, "up", "up")
				typeText(a, "secreta")
				send(a, "down", "down", "enter")
				assertFits(t, a, w, h, "server activo")
			case "query":
				typeText(a, "SELECT 1")
				send(a, "ctrl+r")
				assertFits(t, a, w, h, "query con resultado")
			case "convert":
				send(a, "enter")
				assertFits(t, a, w, h, "convertir nueva")
				send(a, "down", "down", "down", "enter")
				assertFits(t, a, w, h, "script nueva")
				send(a, "esc", "esc", "down", "enter")
				assertFits(t, a, w, h, "convertir agregar")
				c := a.top().(*convertAdd)
				for c.cur != c.rowGen() {
					send(a, "down")
				}
				assertFits(t, a, w, h, "agregar al final")
				send(a, "enter")
				assertFits(t, a, w, h, "script agregar")
				send(a, "esc")
			case "diagram":
				d := a.top().(*diagramScreen)
				for _, tb := range erTables {
					d.open[tb.name] = true
				}
				for i := 0; i < len(erTables)+1; i++ {
					assertFits(t, a, w, h, "diagrama todo abierto")
					send(a, "down")
				}
			}
			for len(a.stack) > 1 {
				send(a, "esc")
			}
		}
	}
}

// Cada cuerpo debe entrar en el espacio disponible sin que se lo recorten
// (fitLines recorta en silencio, así que lo medimos antes).
func TestBodiesFitWithoutTruncation(t *testing.T) {
	a := sized(80, 24)
	ready(t, a)
	limit := minH - 4
	check := func(name string) {
		t.Helper()
		body := a.top().view(a, min(a.w-2, maxCW))
		if n := strings.Count(body, "\n") + 1; n > limit {
			t.Errorf("%s: el cuerpo tiene %d filas y solo caben %d", name, n, limit)
		}
	}
	for _, id := range []string{"open", "server", "query", "convert", "diagram", "gui"} {
		goMain(t, a, id)
		send(a, "enter")
		check(id)
		if id == "server" {
			send(a, "space")
			for i := 0; i < 6; i++ {
				send(a, "down")
			}
			send(a, "enter")
			check("server con error")
			send(a, "up", "up")
			typeText(a, "x")
			send(a, "down", "down", "enter")
			check("server activo")
		}
		if id == "query" {
			typeText(a, "SELECT 1")
			send(a, "ctrl+r")
			check("query con resultado")
		}
		for len(a.stack) > 1 {
			send(a, "esc")
		}
	}
}

// Con ACCESQL_DUMP=1 imprime cada pantalla (sin colores) para revisarla a ojo:
//
//	ACCESQL_DUMP=1 go test ./internal/tui/app -run TestDumpScreens -v
func TestDumpScreens(t *testing.T) {
	if os.Getenv("ACCESQL_DUMP") == "" {
		t.Skip("definí ACCESQL_DUMP=1 para ver las pantallas")
	}
	show := func(title string, a *App) {
		fmt.Printf("\n===== %s =====\n%s\n", title, plain(a))
	}
	a := sized(80, 24)
	show("MENÚ (sin base)", a)
	goMain(t, a, "open")
	send(a, "enter")
	show("ABRIR BASE", a)
	send(a, "enter")
	show("MENÚ (base abierta)", a)
	goMain(t, a, "gen")
	send(a, "enter")
	g := a.top().(*genScreen)
	for i := 0; i < 8; i++ {
		a.Update(genTick{id: g.id})
	}
	show("GENERANDO", a)
	for i := 8; i < len(mockTables); i++ {
		a.Update(genTick{id: g.id})
	}
	show("MENÚ (imagen lista)", a)

	goMain(t, a, "server")
	send(a, "enter", "space")
	show("SERVIDOR (casilla marcada)", a)
	for i := 0; i < 6; i++ {
		send(a, "down")
	}
	send(a, "enter")
	show("SERVIDOR (error)", a)
	send(a, "up", "up")
	typeText(a, "secreta")
	send(a, "down", "down", "enter")
	show("SERVIDOR (activo)", a)
	send(a, "esc")

	goMain(t, a, "query")
	send(a, "enter")
	typeText(a, "SELECT * FROM `Customers` LIMIT 100")
	send(a, "ctrl+r")
	show("CONSULTAS", a)
	send(a, "esc", "esc")

	goMain(t, a, "convert")
	send(a, "enter")
	show("CONVERTIR (menú)", a)
	send(a, "enter")
	show("CONVERTIR · BASE NUEVA", a)
	send(a, "down", "down", "down", "enter")
	show("SCRIPT", a)
	send(a, "esc", "esc", "down", "enter")
	show("CONVERTIR · AGREGAR", a)
	send(a, "esc", "esc")

	goMain(t, a, "diagram")
	send(a, "enter")
	show("DIAGRAMA", a)
	send(a, "esc")

	goMain(t, a, "gui")
	send(a, "enter")
	show("INTERFAZ GRÁFICA", a)
	send(a, "esc")

	b := sized(120, 36)
	show("MENÚ en 120x36 (centrado)", b)
	fmt.Println("\n----- logo (letras = color) -----")
	pix := logoPixels()
	names := map[string]string{"": " ", cTop: "T", cLight: "l", cMid: "m", cDark: "d", cGold: "G"}
	for y := 0; y < logoH; y++ {
		var sb strings.Builder
		for x := 0; x < logoW; x++ {
			sb.WriteString(names[pix[y][x]])
		}
		fmt.Println(sb.String())
	}
}
