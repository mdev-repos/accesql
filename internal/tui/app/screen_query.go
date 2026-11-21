package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// queryScreen: editor SQL, botones y tabla de resultados (simulados).
type queryScreen struct {
	ed      textarea.Model
	inEd    bool // el foco está en el editor (si no, en los botones)
	btn     int
	ran     bool
	ranAt   time.Time
	ranText string
}

var queryButtons = []string{"Ejecutar", "Limpiar", "Exportar JSON", "Exportar SQL", "Volver"}

func newQueryScreen() *queryScreen {
	ed := textarea.New()
	ed.ShowLineNumbers = false
	ed.Prompt = ""
	ed.Placeholder = "SELECT * FROM `Customers` LIMIT 100"
	ed.SetHeight(4)
	ed.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ed.Focus()
	return &queryScreen{ed: ed, inEd: true}
}

func (s *queryScreen) crumb() string { return "Consultas SQL" }
func (s *queryScreen) help() string {
	if s.inEd {
		return "Ctrl+R ejecutar · Tab botones · Esc botones"
	}
	return "←/→ elegir · Enter aceptar · Tab editor · Esc volver"
}

func (s *queryScreen) update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		s.ed, cmd = s.ed.Update(msg) // parpadeo del cursor
		return cmd
	}
	key := k.String()

	if s.inEd {
		switch key {
		case "ctrl+r":
			s.run(a)
			return nil
		case "tab", "esc":
			s.inEd = false
			s.ed.Blur()
			return nil
		}
		var cmd tea.Cmd
		s.ed, cmd = s.ed.Update(msg)
		return cmd
	}

	switch {
	case key == "ctrl+r":
		s.run(a)
	case key == "left" || key == "h":
		s.btn = (s.btn + len(queryButtons) - 1) % len(queryButtons)
	case key == "right" || key == "l":
		s.btn = (s.btn + 1) % len(queryButtons)
	case key == "tab":
		s.inEd = true
		return s.ed.Focus()
	case key == "esc":
		a.pop()
	case key == "enter":
		switch queryButtons[s.btn] {
		case "Ejecutar":
			s.run(a)
		case "Limpiar":
			s.ed.Reset()
			s.ran = false
			s.inEd = true
			return s.ed.Focus()
		case "Exportar JSON":
			a.say("(maqueta) Se guardaría resultado.json")
		case "Exportar SQL":
			a.say("(maqueta) Se guardaría resultado.sql")
		case "Volver":
			a.pop()
		}
	}
	return nil
}

func (s *queryScreen) run(a *App) {
	if strings.TrimSpace(s.ed.Value()) == "" {
		a.alert("Escribí una consulta primero.")
		return
	}
	// Cada ejecución reemplaza el resultado anterior.
	s.ran, s.ranAt, s.ranText = true, time.Now(), strings.TrimSpace(s.ed.Value())
}

func (s *queryScreen) view(a *App, cw int) string {
	s.ed.SetWidth(cw - 4)
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colMuted).Padding(0, 1)
	if s.inEd {
		border = border.BorderForeground(colGold)
	}

	names := make([]string, len(mockTables))
	for i, t := range mockTables {
		names[i] = t.name
	}
	L := []string{
		clip(sMuted.Render(fmt.Sprintf("Tablas (%d): ", len(mockTables)))+sGold.Render(strings.Join(names, ", ")), cw),
		border.Render(s.ed.View()),
		buttons(queryButtons, s.btn, !s.inEd),
		"",
	}
	if s.ran {
		L = append(L,
			sOK.Render("✔ ")+sMuted.Render(fmt.Sprintf("%d filas · 12 ms · datos de ejemplo · ejecutada a las %s", len(sampleRows), s.ranAt.Format("15:04:05"))),
			renderTable(sampleCols, sampleRows, cw))
	} else {
		L = append(L, sMuted.Render("El resultado aparecerá acá."))
	}
	return strings.Join(L, "\n")
}

// renderTable dibuja una tabla de texto: encabezado dorado, números a la derecha.
func renderTable(cols []string, rows [][]any, maxW int) string {
	cell := func(v any) (string, bool) {
		switch x := v.(type) {
		case nil:
			return "NULL", false
		case int, float64:
			return fmt.Sprint(x), true
		}
		return fmt.Sprint(v), false
	}
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = lipgloss.Width(c)
	}
	for _, r := range rows {
		for i, v := range r {
			t, _ := cell(v)
			w[i] = min(18, max(w[i], lipgloss.Width(t)))
		}
	}
	sep := sMuted.Render(" │ ")
	var hdr, rule []string
	for i, c := range cols {
		hdr = append(hdr, sGoldBold.Render(padRight(clip(c, w[i]), w[i])))
		rule = append(rule, strings.Repeat("─", w[i]))
	}
	lines := []string{clip(strings.Join(hdr, sep), maxW), sMuted.Render(clip(strings.Join(rule, "─┼─"), maxW))}
	for _, r := range rows {
		var parts []string
		for i, v := range r {
			t, num := cell(v)
			t = clip(t, w[i])
			switch {
			case v == nil:
				parts = append(parts, sDisabled.Render(padRight(t, w[i])))
			case num:
				parts = append(parts, strings.Repeat(" ", max(0, w[i]-lipgloss.Width(t)))+t)
			default:
				parts = append(parts, padRight(t, w[i]))
			}
		}
		lines = append(lines, clip(strings.Join(parts, sep), maxW))
	}
	return strings.Join(lines, "\n")
}
