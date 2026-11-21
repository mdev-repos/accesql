package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	minW  = 80 // tamaño mínimo de la terminal
	minH  = 24
	maxCW = 78 // ancho máximo del contenido; si la ventana es mayor, se centra
	menuW = 54 // ancho del menú principal
)

// screen es una pantalla de la aplicación. Se apilan: Enter en una opción
// apila una pantalla nueva y VOLVER (o Esc) la quita.
type screen interface {
	update(a *App, msg tea.Msg) tea.Cmd
	view(a *App, cw int) string // cuerpo, sin cabecera ni pie
	crumb() string              // título para la cabecera ("" = pantalla principal)
	help() string               // teclas del pie
}

// shared es el estado que comparten todas las pantallas.
type shared struct {
	filePath   string
	imageReady bool
	serverAddr string // "" si el servidor no está activo
	flash      string // aviso corto que se muestra sobre el pie
	flashErr   bool
}

// App es el modelo raíz de bubbletea.
type App struct {
	w, h  int
	stack []screen
	st    shared
}

// New crea la aplicación con el menú principal como única pantalla.
func New() *App {
	a := &App{}
	a.stack = []screen{newMain()}
	return a
}

func (a *App) top() screen { return a.stack[len(a.stack)-1] }

// push apila una pantalla y devuelve su comando de arranque (si lo tiene).
func (a *App) push(s screen) tea.Cmd {
	a.stack = append(a.stack, s)
	if i, ok := s.(interface{ init() tea.Cmd }); ok {
		return i.init()
	}
	return nil
}

// pop quita la pantalla actual (la principal nunca se quita).
func (a *App) pop() {
	if len(a.stack) > 1 {
		a.stack = a.stack[:len(a.stack)-1]
	}
}

func (a *App) say(msg string)   { a.st.flash, a.st.flashErr = msg, false }
func (a *App) alert(msg string) { a.st.flash, a.st.flashErr = msg, true }

// Init implementa tea.Model.
func (a *App) Init() tea.Cmd { return nil }

// Update implementa tea.Model: reparte cada evento a la pantalla activa.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
		return a, nil
	case tea.KeyMsg:
		if m.String() == "ctrl+c" {
			return a, tea.Quit
		}
		a.st.flash = "" // el aviso dura hasta la próxima tecla
	}
	return a, a.top().update(a, msg)
}

// View implementa tea.Model: dibuja la pantalla completa.
func (a *App) View() string {
	if a.w == 0 || a.h == 0 {
		return ""
	}
	if a.w < minW || a.h < minH {
		msg := lipgloss.JoinVertical(lipgloss.Center,
			sErr.Render("La terminal es muy chica"),
			sMuted.Render(fmt.Sprintf("Accesql necesita al menos %d×%d (ahora %d×%d).", minW, minH, a.w, a.h)),
			sMuted.Render("Agrandá la ventana para continuar."))
		return lipgloss.Place(a.w, a.h, lipgloss.Center, lipgloss.Center, msg)
	}

	s := a.top()
	cw := min(a.w-2, maxCW)

	flash := ""
	if a.st.flash != "" {
		if a.st.flashErr {
			flash = sErr.Render(a.st.flash)
		} else {
			flash = sOK.Render(a.st.flash)
		}
	}
	help := helpLine(s.help())

	var block string
	if s.crumb() == "" {
		block = lipgloss.JoinVertical(lipgloss.Center, s.view(a, cw), flash, help)
	} else {
		bodyH := min(a.h, minH) - 4 // cabecera (2) + aviso (1) + pie (1)
		body := fitLines(s.view(a, cw), cw, bodyH)
		header := sHeader.Width(cw).Render(" Accesql › " + s.crumb())
		rule := sGold.Render(strings.Repeat("━", cw))
		block = lipgloss.JoinVertical(lipgloss.Left, header, rule, body,
			lipgloss.PlaceHorizontal(cw, lipgloss.Center, flash),
			lipgloss.PlaceHorizontal(cw, lipgloss.Center, help))
	}
	return lipgloss.Place(a.w, a.h, lipgloss.Center, lipgloss.Center, block)
}

// fitLines recorta el cuerpo a cw columnas y lo deja de exactamente h filas,
// para que el pie no salte de lugar cuando cambia el contenido.
func fitLines(body string, cw, h int) string {
	lines := strings.Split(body, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = clip(lines[i], cw)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// helpLine arma el pie con las teclas en dorado: "↑/↓ mover · Enter elegir".
func helpLine(h string) string {
	parts := strings.Split(h, " · ")
	for i, p := range parts {
		if k, rest, ok := strings.Cut(p, " "); ok {
			parts[i] = sGoldBold.Render(k) + " " + sMuted.Render(rest)
		} else {
			parts[i] = sMuted.Render(p)
		}
	}
	return strings.Join(parts, sMuted.Render(" · "))
}
