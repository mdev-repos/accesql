package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"access2mysql/internal/session"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestScreenshots guarda cada pantalla del menú, con sus colores, en archivos
// .ansi dentro de la carpeta indicada por ACCESQL_SCREENSHOTS. Es el primer paso
// de scripts/screenshots.sh, que los convierte a SVG para el README. Se corre
// SOLA (go test -run TestScreenshots) para que el logo se dibuje con color.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("ACCESQL_SCREENSHOTS")
	if dir == "" {
		t.Skip("definí ACCESQL_SCREENSHOTS=<carpeta> para generar las capturas")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	shot := func(name string, a *App) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(a.View()), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Inicio, sin ninguna base abierta.
	empty, _ := newApp(t)
	shot("tui-inicio", empty)

	// 2. Con la imagen generada y el servidor MySQL activo.
	a, sess := newReady(t)
	port := freePort(t)
	if err := sess.StartServer(session.ServerOptions{Host: "127.0.0.1", Port: port, User: "access", Password: "una-clave"}); err != nil {
		t.Fatal(err)
	}
	shot("tui-menu", a)

	// 3. Servidor MySQL.
	enter(t, a, "server")
	shot("tui-servidor", a)
	send(a, "esc")

	// 4. Consultas con un JOIN.
	enter(t, a, "query")
	qs := a.top().(*queryScreen)
	typeText(a, "SELECT c.Company, COUNT(o.`Order ID`) AS pedidos FROM Customers c LEFT JOIN Orders o ON o.`Customer ID` = c.ID GROUP BY c.Company ORDER BY pedidos DESC")
	settle(t, a, press(a, "ctrl+r"), func() bool { return !qs.running && qs.res != nil })
	shot("tui-consultas", a)
	send(a, "esc", "esc")

	// 5. Script de una base nueva, mostrando una tabla con su clave y relaciones.
	enter(t, a, "convert")
	send(a, "enter")
	cn := a.top().(*convertNew)
	for cn.focus != 4 {
		send(a, "down")
	}
	settle(t, a, press(a, "enter"), func() bool { _, ok := a.top().(*scriptScreen); return ok })
	ss := a.top().(*scriptScreen)
	for i, l := range ss.lines {
		if strings.HasPrefix(l, "CREATE TABLE `Orders` (") {
			ss.off = max(0, i-2)
			break
		}
	}
	shot("tui-convertir", a)
	send(a, "esc", "esc", "esc")

	// 6. Diagrama con la tabla Orders desplegada.
	cmd := enter(t, a, "diagram")
	ds := a.top().(*diagramScreen)
	settle(t, a, cmd, func() bool { return ds.sc != nil })
	for ds.sc.Tables[ds.cur].Name != "Orders" {
		send(a, "down")
	}
	send(a, "enter")
	shot("tui-diagrama", a)
	_ = strconv.Itoa
}
