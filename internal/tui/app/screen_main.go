package app

import (
	"fmt"
	"strings"

	"access2mysql/internal/version"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// mainScreen es el menú principal: logo, título y las opciones.
type mainScreen struct{ m menu }

func newMain() *mainScreen { return &mainScreen{} }

// refresh recalcula qué opciones están habilitadas según el estado de la sesión.
func (s *mainScreen) refresh(a *App) {
	st := a.sess.State()
	hasFile := st.File != ""
	ready := st.ImageReady
	lock := func(cond bool, why string) (bool, string) {
		if cond {
			return true, why
		}
		return false, ""
	}
	oD, oH := lock(st.Generating, "generando la imagen…")
	gD, gH := lock(!hasFile || st.Generating, "abrí una base primero")
	if st.Generating {
		gH = "en curso…"
	}
	iD, iH := lock(!ready, "generá la imagen primero")
	s.m.setItems([]item{
		{id: "open", label: "Abrir base de datos", disabled: oD, hint: oH},
		{id: "gen", label: "Generar imagen MySQL", disabled: gD, hint: gH},
		{id: "server", label: "Servidor MySQL", disabled: iD, hint: iH},
		{id: "query", label: "Consultas SQL", disabled: iD, hint: iH},
		{id: "convert", label: "Convertir a MySQL", disabled: iD, hint: iH},
		{id: "diagram", label: "Diagrama de relaciones", disabled: iD, hint: iH},
		{id: "gui", label: "Abrir interfaz gráfica"},
		{id: "quit", label: "Cerrar"},
	})
}

func (s *mainScreen) crumb() string { return "" }
func (s *mainScreen) help() string  { return "↑/↓ mover · Enter elegir · q cerrar" }

func (s *mainScreen) update(a *App, msg tea.Msg) tea.Cmd {
	s.refresh(a)
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key := k.String(); {
	case isUp(key):
		s.m.move(-1)
	case isDown(key):
		s.m.move(1)
	case key == "q":
		return tea.Quit
	case key == "enter":
		switch s.m.current() {
		case "open":
			return a.push(newFileScreen(a))
		case "gen":
			return a.push(newGenScreen(a))
		case "server":
			return a.push(newServerScreen(a))
		case "query":
			return a.push(newQueryScreen(a))
		case "convert":
			return a.push(newConvertMenu())
		case "diagram":
			return a.push(newDiagramScreen(a))
		case "gui":
			return a.push(newGUIScreen(a))
		case "quit":
			return tea.Quit
		}
	}
	return nil
}

func (s *mainScreen) view(a *App, cw int) string {
	s.refresh(a)
	title := sTitle.Render("Accesql") + "  " + sVersion.Render("v"+version.Version)
	tag := sMuted.Render("Convierte bases de Microsoft Access a MySQL")
	l1, l2 := statusLines(a)
	return lipgloss.JoinVertical(lipgloss.Center,
		logo(), title, tag, "", clip(l1, a.w-2), clip(l2, a.w-2), s.m.view(menuW))
}

// statusLines resume el estado actual bajo el título: la primera línea habla de
// la base y la imagen, la segunda (si hace falta) de lo que está activo.
func statusLines(a *App) (string, string) {
	st := a.sess.State()
	switch {
	case st.File == "":
		return sMuted.Render("Ninguna base abierta"), ""
	case st.Generating:
		return sMuted.Render("Base: ") + sTitle.Render(baseName(st.File)) + sMuted.Render(" · generando la imagen…"), ""
	case !st.ImageReady && st.GenError != "":
		return sErr.Render("✖ No se pudo generar: " + oneLine(st.GenError)), ""
	case !st.ImageReady:
		return sMuted.Render("Base: ") + sTitle.Render(baseName(st.File)), ""
	}
	l1 := sOK.Render("✔ Imagen «"+st.Database+"» generada") +
		sMuted.Render(fmt.Sprintf(" · %d tablas · %s filas", st.Tables, thousands(st.Rows)))
	var active []string
	if st.Server.Running {
		active = append(active, sOK.Render("● ")+sMuted.Render("servidor MySQL en "+st.Server.Address))
	}
	if a.webRunning() {
		active = append(active, sOK.Render("● ")+sMuted.Render("interfaz web"))
	}
	return l1, strings.Join(active, sMuted.Render("  ·  "))
}

// oneLine deja un texto en una sola línea.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// thousands escribe un entero con punto como separador de miles (1.234).
func thousands(n int64) string {
	s := fmt.Sprint(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return b.String()
}
