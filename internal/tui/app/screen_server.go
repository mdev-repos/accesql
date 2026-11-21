package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// serverScreen: formulario para "exponer" la imagen como servidor MySQL.
// Filas: 0 casilla · 1..5 campos · 6 iniciar/detener · 7 volver.
type serverScreen struct {
	on      bool
	running bool
	in      [5]textinput.Model // dirección, puerto, usuario, contraseña, base
	focus   int
	err     string
}

var serverLabels = [5]string{"Dirección", "Puerto", "Usuario", "Contraseña", "Base"}

const (
	rowToggle = 0
	rowAction = 6
	rowBack   = 7
	fieldW    = 26
)

func newServerScreen() *serverScreen {
	s := &serverScreen{}
	vals := [5]string{"127.0.0.1", "3306", "access", "", "access"}
	for i := range s.in {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 64
		ti.Width = fieldW
		ti.TextStyle = sField
		ti.PlaceholderStyle = sField.Foreground(colMuted)
		ti.SetValue(vals[i])
		s.in[i] = ti
	}
	s.in[3].EchoMode = textinput.EchoPassword
	s.in[3].EchoCharacter = '•'
	s.in[3].Placeholder = "obligatoria"
	return s
}

func (s *serverScreen) crumb() string { return "Servidor MySQL" }
func (s *serverScreen) help() string {
	return "↑/↓ mover · Enter/Espacio elegir · Esc volver"
}

// enabled dice si una fila admite foco en el estado actual.
func (s *serverScreen) enabled(i int) bool {
	switch {
	case i == rowToggle:
		return !s.running
	case i >= 1 && i <= 5:
		return s.on && !s.running
	case i == rowAction:
		return s.on
	}
	return true
}

func (s *serverScreen) setFocus(i int) tea.Cmd {
	s.focus = i
	var cmd tea.Cmd
	for n := range s.in {
		s.in[n].Blur()
	}
	if i >= 1 && i <= 5 {
		cmd = s.in[i-1].Focus()
	}
	return cmd
}

func (s *serverScreen) move(d int) tea.Cmd {
	for i := 0; i < 8; i++ {
		s.focus = (s.focus + d + 8) % 8
		if s.enabled(s.focus) {
			break
		}
	}
	return s.setFocus(s.focus)
}

func (s *serverScreen) validate() string {
	host := strings.TrimSpace(s.in[0].Value())
	port, perr := strconv.Atoi(strings.TrimSpace(s.in[1].Value()))
	switch {
	case host == "":
		return "Escribí la dirección."
	case perr != nil || port < 1 || port > 65535:
		return "El puerto debe ser un número entre 1 y 65535."
	case strings.TrimSpace(s.in[2].Value()) == "":
		return "Escribí el usuario."
	case s.in[3].Value() == "":
		return "La contraseña es obligatoria: sin ella cualquiera podría leer los datos."
	case strings.TrimSpace(s.in[4].Value()) == "":
		return "Escribí el nombre de la base."
	}
	return ""
}

func (s *serverScreen) update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		// Mensajes internos (parpadeo del cursor): los necesita el campo activo.
		if s.focus >= 1 && s.focus <= 5 {
			var cmd tea.Cmd
			s.in[s.focus-1], cmd = s.in[s.focus-1].Update(msg)
			return cmd
		}
		return nil
	}
	key := k.String()
	switch {
	case key == "esc":
		a.pop()
		return nil
	case key == "up" || key == "shift+tab":
		return s.move(-1)
	case key == "down" || key == "tab":
		return s.move(1)
	}
	editing := s.focus >= 1 && s.focus <= 5
	switch {
	case editing && key == "enter":
		return s.move(1)
	case editing:
		var cmd tea.Cmd
		s.in[s.focus-1], cmd = s.in[s.focus-1].Update(msg)
		return cmd
	case isUp(key):
		return s.move(-1)
	case isDown(key):
		return s.move(1)
	case key == "enter" || key == " ":
		switch s.focus {
		case rowToggle:
			s.on = !s.on
		case rowAction:
			s.startStop(a)
		case rowBack:
			a.pop()
		}
	}
	return nil
}

func (s *serverScreen) startStop(a *App) {
	if s.running {
		s.running = false
		a.st.serverAddr = ""
		a.say("Servidor detenido.")
		return
	}
	if s.err = s.validate(); s.err != "" {
		return
	}
	s.running = true
	a.st.serverAddr = fmt.Sprintf("%s:%s", strings.TrimSpace(s.in[0].Value()), strings.TrimSpace(s.in[1].Value()))
	a.say("Servidor iniciado (simulado).")
	s.focus = rowAction
}

func (s *serverScreen) fieldRow(i int, cw int) string {
	label := padRight(serverLabels[i], 12)
	foc := s.focus == i+1
	var val string
	if s.enabled(i + 1) {
		val = s.in[i].View()
	} else {
		shown := s.in[i].Value()
		if i == 3 {
			shown = strings.Repeat("•", len([]rune(shown)))
		}
		val = sDisabled.Render(padRight(shown, fieldW+1))
	}
	if foc {
		return sSelMark.Render(" ▸ ") + sSel.Render(label) + " " + val
	}
	if !s.enabled(i + 1) {
		return "   " + sDisabled.Render(label) + " " + val
	}
	return "   " + label + " " + val
}

func (s *serverScreen) view(a *App, cw int) string {
	hostHint := "Solo esta computadora."
	switch strings.TrimSpace(s.in[0].Value()) {
	case "0.0.0.0":
		hostHint = "Toda la red, sin cifrado: usá una contraseña fuerte."
	case "127.0.0.1", "localhost":
	default:
		hostHint = "Una dirección de esta computadora."
	}

	var L []string
	L = append(L, row(check(s.on, "Exponer la imagen como servidor MySQL"), "", s.focus == rowToggle, !s.enabled(rowToggle), cw))
	L = append(L, "     "+sMuted.Render("Para que PHP u otros programas se conecten como a una base MySQL común."), "")
	for i := range s.in {
		L = append(L, s.fieldRow(i, cw))
	}
	L = append(L, "     "+sMuted.Render(hostHint))
	if s.err != "" {
		L = append(L, "   "+sErr.Render("✖ "+s.err))
	} else {
		L = append(L, "")
	}
	action := "Iniciar servidor"
	if s.running {
		action = "Detener servidor"
	}
	L = append(L, row(action, statusHint(s), s.focus == rowAction, !s.enabled(rowAction), cw))
	L = append(L, row("Volver", "", s.focus == rowBack, false, cw))

	if s.running {
		host := strings.TrimSpace(s.in[0].Value())
		if host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		port, user, db := strings.TrimSpace(s.in[1].Value()), strings.TrimSpace(s.in[2].Value()), strings.TrimSpace(s.in[4].Value())
		L = append(L, "",
			"   "+sOK.Render("● Activo")+sMuted.Render(" · conectate con:"),
			"   "+sField.Render(padRight(fmt.Sprintf(" mysql -h %s -P %s -u %s -p %s", host, port, user, db), cw-4)),
			"   "+sField.Render(padRight(fmt.Sprintf(" mysql:host=%s;port=%s;dbname=%s;charset=utf8mb4", host, port, db), cw-4)))
	}
	return strings.Join(L, "\n")
}

func statusHint(s *serverScreen) string {
	if s.running {
		return "activo"
	}
	if !s.on {
		return "marcá la casilla primero"
	}
	return ""
}
