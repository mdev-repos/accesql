package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"access2mysql/internal/session"
	"access2mysql/internal/web"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	minW  = 80 // tamaño mínimo de la terminal
	minH  = 24
	maxCW = 78 // ancho máximo del contenido; si la ventana es mayor, se centra
	menuW = 54 // ancho del menú principal

	bodyH        = minH - 4  // filas disponibles para el cuerpo de una pantalla interna
	previewBytes = 128 << 10 // tamaño máximo de la vista previa de un script
)

// openBrowser abre un enlace en el navegador; es una variable para que los
// tests no abran uno de verdad.
var openBrowser = web.OpenBrowser

// nextID da un número único a cada pantalla con tareas en segundo plano: sus
// mensajes de respuesta llevan ese número, y así una pantalla ya cerrada no
// recibe respuestas ajenas.
var idCounter atomic.Int64

func nextID() int { return int(idCounter.Add(1)) }

// screen es una pantalla de la aplicación. Se apilan: Enter en una opción
// apila una pantalla nueva y VOLVER (o Esc) la quita.
type screen interface {
	update(a *App, msg tea.Msg) tea.Cmd
	view(a *App, cw int) string // cuerpo, sin cabecera ni pie
	crumb() string              // título para la cabecera ("" = pantalla principal)
	help() string               // teclas del pie
}

// App es el modelo raíz de bubbletea. El estado de la base (archivo abierto,
// imagen generada, servidor MySQL) vive en la sesión, que comparte con la
// interfaz web: lo que se hace en una se ve en la otra.
type App struct {
	w, h  int
	stack []screen
	sess  *session.Session
	web   *web.Running // interfaz gráfica abierta desde este menú, o nil

	flash    string // aviso corto que se muestra sobre el pie
	flashErr bool
}

// New crea la aplicación con el menú principal como única pantalla.
func New(sess *session.Session) *App {
	a := &App{sess: sess}
	a.stack = []screen{newMain()}
	return a
}

// Close detiene lo que la aplicación haya levantado (la interfaz web). La
// sesión la cierra quien la creó.
func (a *App) Close() {
	if a.web != nil {
		a.web.Shutdown()
		a.web = nil
	}
}

// webRunning dice si la interfaz web abierta desde este menú sigue activa.
func (a *App) webRunning() bool {
	if a.web == nil {
		return false
	}
	select {
	case <-a.web.Done():
		a.web = nil
		return false
	default:
		return true
	}
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

func (a *App) say(msg string)   { a.flash, a.flashErr = msg, false }
func (a *App) alert(msg string) { a.flash, a.flashErr = msg, true }

// saveDir es la carpeta donde se guardan los archivos que genera la interfaz
// (resultados y scripts): junto a la base abierta, o si no se puede escribir
// ahí, la carpeta de trabajo y por último la del usuario.
func (a *App) saveDir() string {
	var cands []string
	if f := a.sess.State().File; f != "" {
		cands = append(cands, filepath.Dir(f))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, wd)
	}
	cands = append(cands, session.DefaultDir())
	for _, d := range cands {
		if f, err := os.CreateTemp(d, ".accesql-*"); err == nil {
			f.Close()
			os.Remove(f.Name())
			return d
		}
	}
	return "."
}

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
		a.flash = "" // el aviso dura hasta la próxima tecla
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
	if a.flash != "" {
		if a.flashErr {
			flash = sErr.Render(clip(a.flash, cw))
		} else {
			flash = sOK.Render(clip(a.flash, cw))
		}
	}
	help := helpLine(s.help())

	var block string
	if s.crumb() == "" {
		block = lipgloss.JoinVertical(lipgloss.Center, s.view(a, cw), flash, help)
	} else {
		body := fitLines(s.view(a, cw), cw, bodyH) // cabecera (2) + aviso (1) + pie (1) quedan fuera
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

// baseName devuelve el nombre del archivo de una ruta.
func baseName(path string) string { return filepath.Base(path) }

// fmtSize escribe un tamaño en bytes de forma legible.
func fmtSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%d KB", n>>10)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
}
