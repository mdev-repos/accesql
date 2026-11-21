package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// mainScreen es el menú principal: logo, título y las opciones.
type mainScreen struct{ m menu }

func newMain() *mainScreen {
	s := &mainScreen{}
	s.refresh(&App{})
	return s
}

// refresh recalcula qué opciones están habilitadas según el estado.
func (s *mainScreen) refresh(a *App) {
	hasFile := a.st.filePath != ""
	ready := a.st.imageReady
	lock := func(cond bool, why string) (bool, string) {
		if cond {
			return true, why
		}
		return false, ""
	}
	gD, gH := lock(!hasFile, "abrí una base primero")
	iD, iH := lock(!ready, "generá la imagen primero")
	s.m.setItems([]item{
		{id: "open", label: "Abrir base de datos"},
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
			return a.push(newFileScreen())
		case "gen":
			return a.push(newGenScreen(a))
		case "server":
			return a.push(newServerScreen())
		case "query":
			return a.push(newQueryScreen())
		case "convert":
			return a.push(newConvertMenu())
		case "diagram":
			return a.push(newDiagramScreen())
		case "gui":
			return a.push(newGUIScreen())
		case "quit":
			return tea.Quit
		}
	}
	return nil
}

func (s *mainScreen) view(a *App, cw int) string {
	s.refresh(a)
	title := sTitle.Render("Accesql") + "  " + sVersion.Render("v"+Version)
	tag := sMuted.Render("Convierte bases de Microsoft Access a MySQL")
	return lipgloss.JoinVertical(lipgloss.Center,
		logo(), title, tag, "", statusLine(a), "", s.m.view(menuW))
}

// statusLine resume el estado actual bajo el título.
func statusLine(a *App) string {
	switch {
	case a.st.filePath == "":
		return sMuted.Render("Ninguna base abierta")
	case !a.st.imageReady:
		return sMuted.Render("Base: ") + sTitle.Render(baseName(a.st.filePath))
	}
	parts := []string{sOK.Render("✔ Imagen MySQL generada") +
		sMuted.Render(fmt.Sprintf(" · %d tablas · %d filas", len(mockTables), mockTotalRows()))}
	if a.st.serverAddr != "" {
		parts = append(parts, sOK.Render("● ")+sMuted.Render("servidor "+a.st.serverAddr))
	}
	return strings.Join(parts, sMuted.Render("  ·  "))
}
