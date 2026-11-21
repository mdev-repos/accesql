package app

import (
	"fmt"
	"strings"

	"accesql/internal/session"

	tea "github.com/charmbracelet/bubbletea"
)

type schemaDone struct {
	id  int
	sc  *session.Schema
	err error
}

// diagramScreen: el "diagrama" es un árbol de tablas que se despliegan para ver
// sus columnas y con qué otras tablas se relacionan (las relaciones reales
// definidas en Access). Con decenas de tablas, un dibujo con cajas y líneas no
// entraría en la terminal.
type diagramScreen struct {
	id   int
	sess *session.Session
	sc   *session.Schema
	err  string
	open map[string]bool
	cur  int  // índice entre las filas seleccionables (tablas + Volver)
	top  int  // primera línea visible
	all  bool // mostrar todas las columnas de cada tabla
	free bool // el usuario se desplaza con PgUp/PgDn: no seguir al cursor
}

const diagramRows = 17

// Tope de columnas que se muestran por tabla desplegada (con c se ven todas).
const maxColsShown = 6

func newDiagramScreen(a *App) *diagramScreen {
	return &diagramScreen{id: nextID(), sess: a.sess, open: map[string]bool{}}
}

// init pide el esquema en segundo plano: la primera vez recorre las tablas para
// confirmar las claves primarias, y con bases grandes puede tardar.
func (s *diagramScreen) init() tea.Cmd {
	id, sess := s.id, s.sess
	return func() tea.Msg {
		sc, err := sess.Schema()
		return schemaDone{id, sc, err}
	}
}

func (s *diagramScreen) crumb() string { return "Diagrama de relaciones" }
func (s *diagramScreen) help() string {
	return "↑/↓ mover · Enter abrir · c columnas · +/- todas · PgUp/PgDn · Esc volver"
}

// dline es una línea del árbol; target >= 0 si se puede seleccionar.
type dline struct {
	text   string
	target int
}

func (s *diagramScreen) relationsOf(name string) (out, in []string) {
	for _, r := range s.sc.Relations {
		mark := ""
		if !r.Enforced {
			mark = "*"
		}
		if strings.EqualFold(r.Table, name) {
			out = append(out, fmt.Sprintf("%s (%s → %s)%s", r.RefTable, strings.Join(r.Columns, ", "), strings.Join(r.RefColumns, ", "), mark))
		}
		if strings.EqualFold(r.RefTable, name) && !strings.EqualFold(r.Table, name) {
			in = append(in, fmt.Sprintf("%s (%s)%s", r.Table, strings.Join(r.Columns, ", "), mark))
		}
	}
	return
}

func (s *diagramScreen) lines(cw int) []dline {
	var L []dline
	for i, t := range s.sc.Tables {
		out, in := s.relationsOf(t.Name)
		mark := "[+]"
		if s.open[t.Name] {
			mark = "[-]"
		}
		rel := "relaciones"
		if len(out)+len(in) == 1 {
			rel = "relación"
		}
		hint := fmt.Sprintf("%d col. · %d %s", len(t.Columns), len(out)+len(in), rel)
		L = append(L, dline{text: row(mark+" "+t.Name, hint, i == s.cur, false, cw), target: i})
		if !s.open[t.Name] {
			continue
		}
		cols := t.Columns
		hidden := 0
		if !s.all && len(cols) > maxColsShown {
			// Primero las claves, para que no queden ocultas.
			sorted := append([]session.SchemaColumn(nil), cols...)
			for j := 1; j < len(sorted); j++ {
				for k := j; k > 0 && key(sorted[k]) > key(sorted[k-1]); k-- {
					sorted[k], sorted[k-1] = sorted[k-1], sorted[k]
				}
			}
			cols, hidden = sorted[:maxColsShown], len(sorted)-maxColsShown
		}
		hasTail := hidden > 0 || len(out)+len(in) > 0
		for j, c := range cols {
			branch := "├─"
			if j == len(cols)-1 && !hasTail {
				branch = "└─"
			}
			tag := "  "
			switch {
			case c.PK:
				tag = sGoldBold.Render("PK")
			case c.FK:
				tag = sGoldBold.Render("FK")
			}
			typ := c.AccessType
			if typ == "" {
				typ = c.Type
			}
			L = append(L, dline{text: "     " + sMuted.Render(branch) + " " + tag + " " + padRight(clip(c.Name, 24), 25) + sMuted.Render(typ), target: -1})
		}
		if hidden > 0 {
			br := "├─"
			if len(out)+len(in) == 0 {
				br = "└─"
			}
			L = append(L, dline{text: "     " + sMuted.Render(br+"    … y "+fmt.Sprint(hidden)+" columnas más"), target: -1})
		}
		for _, grp := range []struct {
			arrow string
			list  []string
		}{{"→", out}, {"←", in}} {
			for j, r := range grp.list {
				br := "├─"
				if grp.arrow == "←" && j == len(grp.list)-1 || grp.arrow == "→" && j == len(grp.list)-1 && len(in) == 0 {
					br = "└─"
				}
				L = append(L, dline{text: "     " + sMuted.Render(br) + " " + sGold.Render(grp.arrow) + " " + clip(r, cw-12), target: -1})
			}
		}
	}
	L = append(L, dline{text: row("Volver", "", s.cur == len(s.sc.Tables), false, cw), target: len(s.sc.Tables)})
	return L
}

func key(c session.SchemaColumn) int {
	switch {
	case c.PK:
		return 2
	case c.FK:
		return 1
	}
	return 0
}

func (s *diagramScreen) update(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case schemaDone:
		if m.id != s.id {
			return nil
		}
		if m.err != nil {
			s.err = oneLine(m.err.Error())
		} else {
			s.sc = m.sc
		}
	case tea.KeyMsg:
		if s.sc == nil { // todavía cargando, o con error: solo se puede volver
			if m.String() == "esc" || m.String() == "enter" {
				a.pop()
			}
			return nil
		}
		return s.key(a, m)
	}
	return nil
}

func (s *diagramScreen) key(a *App, k tea.KeyMsg) tea.Cmd {
	n := len(s.sc.Tables) + 1
	switch k.String() {
	case "pgdown":
		s.free = true
		s.top += diagramRows - 2
		return nil
	case "pgup":
		s.free = true
		s.top = max(0, s.top-(diagramRows-2))
		return nil
	case "c":
		s.all = !s.all
		return nil
	}
	s.free = false
	switch key := k.String(); {
	case key == "esc":
		a.pop()
	case isUp(key):
		s.cur = (s.cur - 1 + n) % n
	case isDown(key):
		s.cur = (s.cur + 1) % n
	case key == "right" || key == "l":
		if s.cur < len(s.sc.Tables) {
			s.open[s.sc.Tables[s.cur].Name] = true
		}
	case key == "left" || key == "h":
		if s.cur < len(s.sc.Tables) {
			s.open[s.sc.Tables[s.cur].Name] = false
		}
	case key == "+":
		for _, t := range s.sc.Tables {
			s.open[t.Name] = true
		}
	case key == "-":
		s.open = map[string]bool{}
	case key == "enter" || key == " ":
		if s.cur == len(s.sc.Tables) {
			a.pop()
		} else {
			name := s.sc.Tables[s.cur].Name
			s.open[name] = !s.open[name]
		}
	}
	return nil
}

func (s *diagramScreen) view(a *App, cw int) string {
	switch {
	case s.err != "":
		return sErr.Render("✖ "+clip(s.err, cw-3)) + "\n\n" + sMuted.Render("Esc para volver.")
	case s.sc == nil:
		return sMuted.Render("Leyendo la estructura y confirmando las claves con los datos…")
	}
	L := s.lines(cw)
	if !s.free {
		// Se muestra el bloque completo de la tabla del cursor (sus columnas y
		// relaciones) si entra; si es más largo, arranca en su encabezado.
		at, blockEnd := 0, len(L)
		for i, l := range L {
			if l.target == s.cur {
				at, blockEnd = i, len(L)
				for j := i + 1; j < len(L); j++ {
					if L[j].target >= 0 {
						blockEnd = j
						break
					}
				}
				break
			}
		}
		switch {
		case blockEnd-at > diagramRows:
			s.top = at
		case at < s.top:
			s.top = at
		case blockEnd > s.top+diagramRows:
			s.top = blockEnd - diagramRows
		}
	}
	s.top = max(0, min(s.top, len(L)-diagramRows))
	end := min(len(L), s.top+diagramRows)
	out := make([]string, 0, diagramRows+2)
	for _, l := range L[s.top:end] {
		out = append(out, l.text)
	}
	note := fmt.Sprintf("%d relaciones de Access · → apunta a · ← la apuntan · * sin integridad", len(s.sc.Relations))
	if len(s.sc.Relations) == 0 {
		note = "Esta base no define relaciones entre tablas."
	}
	out = append(out, "", sMuted.Render(clip(note, cw)))
	return strings.Join(out, "\n")
}
