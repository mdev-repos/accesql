package app

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"access2mysql/internal/session"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// serverTick hace que la pantalla se redibuje cada segundo: así se nota si el
// servidor se detuvo solo.
type serverTick struct{ id int }

// serverScreen: formulario para exponer la imagen como servidor MySQL.
// Filas: 0 casilla · 1..4 campos · 5 iniciar/detener · 6 volver.
type serverScreen struct {
	id    int
	on    bool
	in    [4]textinput.Model // dirección, puerto, usuario, contraseña
	focus int
	err   string
}

var serverLabels = [4]string{"Dirección", "Puerto", "Usuario", "Contraseña"}

const (
	rowToggle = 0
	rowAction = 5
	rowBack   = 6
	fieldW    = 26
)

func newServerScreen(a *App) *serverScreen {
	s := &serverScreen{id: nextID()}
	vals := [4]string{"127.0.0.1", "3306", "access", ""}
	if sv := a.sess.State().Server; sv.Running { // ya está activo: se muestran sus datos
		s.on = true
		if h, p, err := net.SplitHostPort(sv.Address); err == nil {
			vals[0], vals[1] = h, p
		}
		vals[2] = sv.User
	}
	for i := range s.in {
		ti := newFieldInput(vals[i], fieldW)
		s.in[i] = ti
	}
	s.in[3].EchoMode = textinput.EchoPassword
	s.in[3].EchoCharacter = '•'
	s.in[3].Placeholder = "obligatoria"
	return s
}

func (s *serverScreen) init() tea.Cmd { return s.tick() }

func (s *serverScreen) tick() tea.Cmd {
	id := s.id
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return serverTick{id} })
}

func (s *serverScreen) crumb() string { return "Servidor MySQL" }
func (s *serverScreen) help() string {
	return "↑/↓ mover · Enter/Espacio elegir · Esc volver"
}

// enabled dice si una fila admite foco en el estado actual.
func (s *serverScreen) enabled(i int, running bool) bool {
	switch {
	case i == rowToggle:
		return !running
	case i >= 1 && i <= 4:
		return s.on && !running
	case i == rowAction:
		return s.on || running
	}
	return true
}

func (s *serverScreen) setFocus(i int) tea.Cmd {
	s.focus = i
	for n := range s.in {
		s.in[n].Blur()
	}
	if i >= 1 && i <= 4 {
		return s.in[i-1].Focus()
	}
	return nil
}

func (s *serverScreen) move(d int, running bool) tea.Cmd {
	for i := 0; i < 7; i++ {
		s.focus = (s.focus + d + 7) % 7
		if s.enabled(s.focus, running) {
			break
		}
	}
	return s.setFocus(s.focus)
}

func (s *serverScreen) options() session.ServerOptions {
	port, _ := strconv.Atoi(strings.TrimSpace(s.in[1].Value()))
	return session.ServerOptions{
		Host: strings.TrimSpace(s.in[0].Value()), Port: port,
		User: strings.TrimSpace(s.in[2].Value()), Password: s.in[3].Value(),
	}
}

func (s *serverScreen) update(a *App, msg tea.Msg) tea.Cmd {
	running := a.sess.State().Server.Running
	if !running && s.focus == rowAction && !s.on {
		s.focus = rowToggle
	}
	switch m := msg.(type) {
	case serverTick:
		if m.id == s.id {
			return s.tick()
		}
		return nil
	case tea.KeyMsg:
		return s.key(a, m, running)
	}
	// Mensajes internos (parpadeo del cursor): los necesita el campo activo.
	if s.focus >= 1 && s.focus <= 4 {
		var cmd tea.Cmd
		s.in[s.focus-1], cmd = s.in[s.focus-1].Update(msg)
		return cmd
	}
	return nil
}

func (s *serverScreen) key(a *App, k tea.KeyMsg, running bool) tea.Cmd {
	key := k.String()
	switch {
	case key == "esc":
		a.pop()
		return nil
	case key == "up" || key == "shift+tab":
		return s.move(-1, running)
	case key == "down" || key == "tab":
		return s.move(1, running)
	}
	editing := s.focus >= 1 && s.focus <= 4
	switch {
	case editing && key == "enter":
		return s.move(1, running)
	case editing:
		var cmd tea.Cmd
		s.in[s.focus-1], cmd = s.in[s.focus-1].Update(k)
		return cmd
	case isUp(key):
		return s.move(-1, running)
	case isDown(key):
		return s.move(1, running)
	case key == "enter" || key == " ":
		switch s.focus {
		case rowToggle:
			s.on = !s.on
		case rowAction:
			s.startStop(a, running)
		case rowBack:
			a.pop()
		}
	}
	return nil
}

func (s *serverScreen) startStop(a *App, running bool) {
	if running {
		a.sess.StopServer()
		s.err = ""
		a.say("Servidor detenido.")
		return
	}
	opts := s.options()
	if msg := session.ValidateServer(opts); msg != "" {
		s.err = msg
		return
	}
	if err := a.sess.StartServer(opts); err != nil {
		s.err = oneLine(err.Error())
		return
	}
	s.err = ""
	a.say("Servidor iniciado.")
}

func (s *serverScreen) fieldRow(i int, running bool) string {
	label := padRight(serverLabels[i], 12)
	var val string
	if s.enabled(i+1, running) {
		val = s.in[i].View()
	} else {
		shown := s.in[i].Value()
		if i == 3 {
			shown = strings.Repeat("•", len([]rune(shown)))
		}
		val = sDisabled.Render(padRight(shown, fieldW+1))
	}
	switch {
	case s.focus == i+1:
		return sSelMark.Render(" ▸ ") + sSel.Render(label) + " " + val
	case !s.enabled(i+1, running):
		return "   " + sDisabled.Render(label) + " " + val
	}
	return "   " + label + " " + val
}

func (s *serverScreen) view(a *App, cw int) string {
	st := a.sess.State()
	running := st.Server.Running
	hostHint := "Solo esta computadora."
	switch strings.TrimSpace(s.in[0].Value()) {
	case "0.0.0.0":
		hostHint = "Toda la red, sin cifrado: usá una contraseña fuerte."
	case "127.0.0.1", "localhost":
	default:
		hostHint = "Una dirección de esta computadora."
	}

	var L []string
	L = append(L, row(check(s.on || running, "Exponer la imagen como servidor MySQL"), "", s.focus == rowToggle, !s.enabled(rowToggle, running), cw))
	L = append(L, "     "+sMuted.Render("Para que PHP u otros programas se conecten como a una base MySQL común."), "")
	for i := range s.in {
		L = append(L, s.fieldRow(i, running))
	}
	L = append(L, "   "+sMuted.Render(padRight("Base", 12)+" ")+sTitle.Render(st.Database)+sMuted.Render("  (se elige al generar la imagen)"))
	L = append(L, "     "+sMuted.Render(hostHint))
	msg := s.err
	if msg == "" && st.Server.Error != "" && !running {
		msg = "El servidor se detuvo: " + oneLine(st.Server.Error)
	}
	if msg == "" {
		L = append(L, "")
	}
	for i, l := range wrap(msg, cw-8, 2) {
		prefix := "   "
		if i == 0 {
			prefix += "✖ "
		} else {
			prefix += "  "
		}
		L = append(L, sErr.Render(prefix+l))
	}
	action, hint := "Iniciar servidor", ""
	switch {
	case running:
		action, hint = "Detener servidor", "activo"
	case !s.on:
		hint = "marcá la casilla primero"
	}
	L = append(L, row(action, hint, s.focus == rowAction, !s.enabled(rowAction, running), cw))
	L = append(L, row("Volver", "", s.focus == rowBack, false, cw))

	if running {
		host, port, _ := net.SplitHostPort(st.Server.Address)
		if host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		L = append(L, "",
			"   "+sOK.Render("● Activo")+sMuted.Render(" · conectate con (la contraseña es la que elegiste):"),
			"   "+sField.Render(padRight(fmt.Sprintf(" mysql -h %s -P %s -u %s -p %s", host, port, st.Server.User, st.Server.Database), cw-4)),
			"   "+sField.Render(padRight(fmt.Sprintf(" mysql:host=%s;port=%s;dbname=%s;charset=utf8mb4", host, port, st.Server.Database), cw-4)))
	}
	return strings.Join(L, "\n")
}
