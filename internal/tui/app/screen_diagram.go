package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// diagramScreen: el "diagrama" es un árbol de tablas que se despliegan para ver
// sus campos y con qué otras tablas se relacionan. En 20 tablas un dibujo con
// cajas y líneas no entraría en la terminal.
type diagramScreen struct {
	open map[string]bool
	cur  int // índice entre las filas seleccionables
	top  int // primera línea visible
}

const diagramRows = 13

func newDiagramScreen() *diagramScreen {
	return &diagramScreen{open: map[string]bool{"Orders": true}}
}

func (s *diagramScreen) crumb() string { return "Diagrama de relaciones" }
func (s *diagramScreen) help() string {
	return "↑/↓ mover · Enter/→ abrir · ← cerrar · Esc volver"
}

// dline es una línea del árbol; target >= 0 si se puede seleccionar.
type dline struct {
	text   string
	target int // índice entre seleccionables, o -1
}

func relationsOf(name string) (out, in []string) {
	for _, r := range erRels {
		if r[0] == name {
			out = append(out, r[2])
		}
		if r[2] == name {
			in = append(in, r[0])
		}
	}
	return
}

func (s *diagramScreen) lines(cw int) []dline {
	var L []dline
	for i, t := range erTables {
		out, in := relationsOf(t.name)
		mark := "[+]"
		if s.open[t.name] {
			mark = "[-]"
		}
		rel := "relaciones"
		if len(out)+len(in) == 1 {
			rel = "relación"
		}
		hint := fmt.Sprintf("%d campos · %d %s", len(t.fields), len(out)+len(in), rel)
		L = append(L, dline{text: row(mark+" "+t.name, hint, i == s.cur, false, cw), target: i})
		if !s.open[t.name] {
			continue
		}
		for j, f := range t.fields {
			branch := "├─"
			if j == len(t.fields)-1 && len(out)+len(in) == 0 {
				branch = "└─"
			}
			key := "  "
			switch f.key {
			case "pk":
				key = sGoldBold.Render("PK")
			case "fk":
				key = sGoldBold.Render("FK")
			}
			ref := ""
			if f.key == "fk" {
				for _, r := range erRels {
					if r[0] == t.name && r[1] == f.name {
						ref = sMuted.Render(" → " + r[2] + "." + r[3])
					}
				}
			}
			L = append(L, dline{text: "     " + sMuted.Render(branch) + " " + key + " " + padRight(f.name, 14) + sMuted.Render(padRight(f.typ, 9)) + ref, target: -1})
		}
		if len(out)+len(in) > 0 {
			L = append(L, dline{text: "     " + sMuted.Render("└─ ") + sGold.Render("Relacionada con: ") + strings.Join(append(append([]string{}, out...), in...), ", "), target: -1})
		}
	}
	L = append(L, dline{text: row("Volver", "", s.cur == len(erTables), false, cw), target: len(erTables)})
	return L
}

func (s *diagramScreen) update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	n := len(erTables) + 1
	switch key := k.String(); {
	case key == "esc":
		a.pop()
	case isUp(key):
		s.cur = (s.cur - 1 + n) % n
	case isDown(key):
		s.cur = (s.cur + 1) % n
	case key == "right" || key == "l":
		if s.cur < len(erTables) {
			s.open[erTables[s.cur].name] = true
		}
	case key == "left" || key == "h":
		if s.cur < len(erTables) {
			s.open[erTables[s.cur].name] = false
		}
	case key == "enter" || key == " ":
		if s.cur == len(erTables) {
			a.pop()
		} else {
			name := erTables[s.cur].name
			s.open[name] = !s.open[name]
		}
	}
	return nil
}

func (s *diagramScreen) view(a *App, cw int) string {
	L := s.lines(cw)
	// Mantener visible la línea del cursor.
	at := 0
	for i, l := range L {
		if l.target == s.cur {
			at = i
		}
	}
	if at < s.top {
		s.top = at
	}
	// Al desplegar una tabla se prefiere ver también sus campos.
	if at >= s.top+diagramRows {
		s.top = at - diagramRows + 1
	}
	if s.top > len(L)-diagramRows {
		s.top = max(0, len(L)-diagramRows)
	}
	end := min(len(L), s.top+diagramRows)
	out := make([]string, 0, diagramRows+2)
	for _, l := range L[s.top:end] {
		out = append(out, l.text)
	}
	out = append(out, "", sMuted.Render("Relaciones sugeridas por nombre de campo · ejemplo con 6 tablas"))
	return strings.Join(out, "\n")
}
