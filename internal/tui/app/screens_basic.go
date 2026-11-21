package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"access2mysql/internal/session"
	"access2mysql/internal/web"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Abrir base de datos: explorador de carpetas
// ---------------------------------------------------------------------------

type fileScreen struct {
	l   session.Listing
	m   menu
	err string
}

func newFileScreen(a *App) *fileScreen {
	s := &fileScreen{}
	s.m.rows = 10
	s.load(a.sess.State().File) // arranca en la carpeta de la base abierta
	if s.l.Path == "" && s.err != "" {
		s.load("") // si esa carpeta ya no existe, en la del usuario
	}
	return s
}

// load lee una carpeta. Si falla, queda el listado anterior y el error a la vista.
func (s *fileScreen) load(path string) {
	l, err := session.ListDir(path)
	if err != nil {
		s.err = err.Error()
		return
	}
	s.err, s.l = "", l
	s.rebuild()
}

// rebuild arma la lista: subir, carpetas, archivos y volver. Los ids apuntan a
// la posición en l.Entries.
func (s *fileScreen) rebuild() {
	var items []item
	if s.l.HasParent {
		items = append(items, item{id: "up", label: "../  (subir una carpeta)"})
	}
	for i, e := range s.l.Entries {
		if e.IsDir {
			items = append(items, item{id: fmt.Sprint("e", i), label: e.Name + "/"})
		}
	}
	first := -1
	for i, e := range s.l.Entries {
		if !e.IsDir {
			if first < 0 {
				first = len(items)
			}
			items = append(items, item{id: fmt.Sprint("e", i), label: e.Name, hint: fmtSize(e.Size)})
		}
	}
	items = append(items, item{id: "back", label: "Volver"})
	s.m = menu{rows: s.m.rows}
	s.m.setItems(items)
	if first >= 0 { // si hay bases, el cursor arranca en la primera
		s.m.cur = first
		s.m.keepVisible()
	}
}

func (s *fileScreen) crumb() string { return "Abrir base de datos" }
func (s *fileScreen) help() string {
	return "↑/↓ mover · Enter abrir · Retroceso subir · Esc volver"
}

func (s *fileScreen) update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key := k.String(); {
	case isUp(key):
		s.m.move(-1)
	case isDown(key):
		s.m.move(1)
	case key == "esc":
		a.pop()
	case key == "backspace" && s.l.HasParent:
		s.load(s.l.Parent)
	case key == "enter":
		id := s.m.current()
		switch {
		case id == "back":
			a.pop()
		case id == "up":
			s.load(s.l.Parent)
		case strings.HasPrefix(id, "e"):
			var n int
			fmt.Sscanf(id, "e%d", &n)
			e := s.l.Entries[n]
			if e.IsDir {
				s.load(e.Path)
				return nil
			}
			if err := a.sess.Open(e.Path); err != nil {
				s.err = err.Error()
				return nil
			}
			a.pop()
			a.say("Base abierta: " + e.Name)
		}
	}
	return nil
}

func (s *fileScreen) view(a *App, cw int) string {
	where := s.l.Path
	if where == "" {
		where = "(unidades del equipo)"
	}
	lines := []string{
		sMuted.Render("Carpeta: ") + sGoldBold.Render(clip(where, cw-10)),
		sMuted.Render("Solo se muestran bases de Access (.accdb, .mdb)."),
	}
	if s.err != "" {
		lines = append(lines, sErr.Render("✖ "+clip(oneLine(s.err), cw-3)))
	} else {
		lines = append(lines, "")
	}
	lines = append(lines, s.m.view(cw))
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Generar imagen MySQL: nombre, progreso y cancelación
// ---------------------------------------------------------------------------

type genTick struct{ id int }
type genDone struct {
	id  int
	err error
}

type genScreen struct {
	id        int
	name      textinput.Model
	focus     int // 0 nombre · 1 generar · 2 volver
	running   bool
	canceling bool
	err       string
}

func newGenScreen(a *App) *genScreen {
	st := a.sess.State()
	def := st.Database // si ya hay una imagen, se propone el mismo nombre
	if def == "" {
		def = st.Suggested
	}
	return &genScreen{id: nextID(), name: newFieldInput(def, 30)}
}

func (s *genScreen) init() tea.Cmd { return s.name.Focus() }

func (s *genScreen) crumb() string { return "Generar imagen MySQL" }
func (s *genScreen) help() string {
	if s.running {
		return "Esc cancelar"
	}
	return "↑/↓ mover · Enter elegir · Esc volver"
}

func (s *genScreen) setFocus(i int) tea.Cmd {
	s.focus = (i + 3) % 3
	s.name.Blur()
	if s.focus == 0 {
		return s.name.Focus()
	}
	return nil
}

func (s *genScreen) tick() tea.Cmd {
	id := s.id
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return genTick{id} })
}

func (s *genScreen) start(a *App) tea.Cmd {
	name := strings.TrimSpace(s.name.Value())
	if !session.ValidDatabaseName(name) {
		s.err = "El nombre solo puede tener letras, números, _ y - (hasta 64 caracteres)."
		return nil
	}
	s.err, s.running = "", true
	id, sess := s.id, a.sess
	run := func() tea.Msg {
		err := sess.Generate(context.Background(), session.GenerateOptions{Database: name}, nil)
		return genDone{id, err}
	}
	return tea.Batch(run, s.tick())
}

func (s *genScreen) update(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case genTick:
		if m.id == s.id && s.running {
			return s.tick()
		}
	case genDone:
		if m.id != s.id {
			return nil
		}
		a.pop()
		switch {
		case m.err == nil:
			a.say("✔ ¡Imagen MySQL generada!")
		case errors.Is(m.err, context.Canceled):
			a.alert("Generación cancelada.")
		default:
			a.alert("No se pudo generar: " + oneLine(m.err.Error()))
		}
	case tea.KeyMsg:
		key := m.String()
		if s.running {
			if key == "esc" && !s.canceling {
				s.canceling = true
				a.sess.CancelGenerate()
			}
			return nil
		}
		switch {
		case key == "esc":
			a.pop()
		case key == "up" || key == "shift+tab":
			return s.setFocus(s.focus - 1)
		case key == "down" || key == "tab":
			return s.setFocus(s.focus + 1)
		case s.focus == 0 && key == "enter":
			return s.setFocus(1)
		case s.focus == 0:
			var cmd tea.Cmd
			s.name, cmd = s.name.Update(msg)
			return cmd
		case isUp(key):
			return s.setFocus(s.focus - 1)
		case isDown(key):
			return s.setFocus(s.focus + 1)
		case key == "enter":
			if s.focus == 1 {
				return s.start(a)
			}
			a.pop()
		}
	default:
		if !s.running && s.focus == 0 { // parpadeo del cursor
			var cmd tea.Cmd
			s.name, cmd = s.name.Update(msg)
			return cmd
		}
	}
	return nil
}

func (s *genScreen) view(a *App, cw int) string {
	st := a.sess.State()
	if s.running {
		pct, text := 0.0, "Abriendo el archivo…"
		if p := st.Progress; p != nil && p.Total > 0 {
			pct = float64(p.Number-1) / float64(p.Total)
			text = fmt.Sprintf("Tabla %d de %d: %s", p.Number, p.Total, p.Table)
		}
		if s.canceling {
			text = "Cancelando…"
		}
		return strings.Join([]string{
			sMuted.Render("Leyendo ") + sTitle.Render(baseName(st.File)) + sMuted.Render(" (solo lectura)"),
			"",
			"  " + progressBar(pct, max(10, cw-12)),
			"",
			"  " + sGold.Render(clip(text, cw-4)),
		}, "\n")
	}

	label := padRight("Nombre de la base", 19)
	nameRow := "   " + label + " " + s.name.View()
	if s.focus == 0 {
		nameRow = sSelMark.Render(" ▸ ") + sSel.Render(label) + " " + s.name.View()
	}
	errLine := ""
	if s.err != "" {
		errLine = "   " + sErr.Render("✖ "+clip(s.err, cw-6))
	}
	note := "Es el nombre que usarán los programas para conectarse (dbname=…)."
	if st.ImageReady {
		note = "Ya hay una imagen: generarla de nuevo la reemplaza (y detiene el servidor MySQL)."
	}
	return strings.Join([]string{
		sMuted.Render("Lee la base de Access y arma la copia que se consulta y se expone."),
		"",
		nameRow,
		"     " + sMuted.Render(clip(note, cw-6)),
		errLine,
		"",
		row("Generar imagen", "", s.focus == 1, false, cw),
		row("Volver", "", s.focus == 2, false, cw),
	}, "\n")
}

// ---------------------------------------------------------------------------
// Abrir interfaz gráfica: la web, sobre la misma sesión
// ---------------------------------------------------------------------------

type guiScreen struct {
	m      menu
	err    string
	opened string
}

func newGUIScreen(a *App) *guiScreen {
	s := &guiScreen{}
	if !a.webRunning() {
		run, err := web.Start(a.sess, "127.0.0.1", 0)
		if err != nil {
			s.err = err.Error()
		} else {
			a.web = run
			if err := openBrowser(run.URL); err != nil {
				s.err = "No se pudo abrir el navegador: copiá el enlace a mano."
			} else {
				s.opened = "Se abrió en tu navegador."
			}
		}
	}
	s.refresh(a)
	return s
}

func (s *guiScreen) refresh(a *App) {
	if a.webRunning() {
		s.m.setItems([]item{
			{id: "open", label: "Abrir de nuevo en el navegador"},
			{id: "stop", label: "Detener la interfaz gráfica"},
			{id: "back", label: "Volver"},
		})
		return
	}
	s.m.setItems([]item{{id: "back", label: "Volver"}})
}

func (s *guiScreen) crumb() string { return "Interfaz gráfica" }
func (s *guiScreen) help() string  { return "↑/↓ mover · Enter elegir · Esc volver" }

func (s *guiScreen) update(a *App, msg tea.Msg) tea.Cmd {
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
	case key == "esc":
		a.pop()
	case key == "enter":
		switch s.m.current() {
		case "open":
			if err := openBrowser(a.web.URL); err != nil {
				s.err = "No se pudo abrir el navegador: copiá el enlace a mano."
			} else {
				s.err, s.opened = "", "Se abrió en tu navegador."
			}
		case "stop":
			a.Close()
			a.pop()
			a.say("Interfaz gráfica detenida.")
		case "back":
			a.pop()
		}
	}
	return nil
}

func (s *guiScreen) view(a *App, cw int) string {
	var L []string
	if a.webRunning() {
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colGold).Padding(0, 1)
		L = append(L,
			sMuted.Render("La interfaz web está activa. Si no se abrió sola, copiá este enlace:"),
			"",
			box.Render(sGoldBold.Render(a.web.URL)),
			"",
			sMuted.Render("Comparte la base ya cargada con este menú: no vuelve a leer el archivo."),
		)
		if !a.web.Loopback {
			L = append(L, sErr.Render("Escucha en la red sin cifrado."))
		}
		if s.opened != "" {
			L = append(L, sOK.Render(s.opened))
		}
	} else {
		L = append(L, sMuted.Render("La interfaz web no está activa."))
	}
	if s.err != "" {
		L = append(L, sErr.Render("✖ "+clip(oneLine(s.err), cw-3)))
	}
	L = append(L, "", s.m.view(cw))
	return strings.Join(L, "\n")
}

// ---------------------------------------------------------------------------
// Elegir una tabla (para escribir su consulta)
// ---------------------------------------------------------------------------

type tablePicker struct {
	m      menu
	choose func(name string)
	err    string
}

func newTablePicker(a *App, choose func(string)) *tablePicker {
	s := &tablePicker{choose: choose}
	s.m.rows = 12
	tables, err := a.sess.Tables()
	if err != nil {
		s.err = err.Error()
	}
	items := make([]item, 0, len(tables)+1)
	for _, t := range tables {
		items = append(items, item{id: t.Name, label: t.Name, hint: thousands(t.Rows) + " filas"})
	}
	items = append(items, item{id: "\x00back", label: "Volver"})
	s.m.setItems(items)
	return s
}

func (s *tablePicker) crumb() string { return "Elegir una tabla" }
func (s *tablePicker) help() string  { return "↑/↓ mover · Enter elegir · Esc volver" }

func (s *tablePicker) update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key := k.String(); {
	case isUp(key):
		s.m.move(-1)
	case isDown(key):
		s.m.move(1)
	case key == "esc":
		a.pop()
	case key == "enter":
		id := s.m.current()
		a.pop()
		if id != "\x00back" {
			s.choose(id)
		}
	}
	return nil
}

func (s *tablePicker) view(a *App, cw int) string {
	head := sMuted.Render("Escribe en el editor la consulta de la tabla elegida.")
	if s.err != "" {
		head = sErr.Render("✖ " + clip(oneLine(s.err), cw-3))
	}
	return head + "\n\n" + s.m.view(cw)
}

// ---------------------------------------------------------------------------
// Convertir a MySQL: submenú
// ---------------------------------------------------------------------------

type convertMenu struct{ m menu }

func newConvertMenu() *convertMenu {
	s := &convertMenu{}
	s.m.setItems([]item{
		{id: "new", label: "Crear una base de datos nueva"},
		{id: "add", label: "Agregar datos a tablas que ya existen"},
		{id: "back", label: "Volver"},
	})
	return s
}

func (s *convertMenu) crumb() string { return "Convertir a MySQL" }
func (s *convertMenu) help() string  { return "↑/↓ mover · Enter elegir · Esc volver" }

func (s *convertMenu) update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key := k.String(); {
	case isUp(key):
		s.m.move(-1)
	case isDown(key):
		s.m.move(1)
	case key == "esc":
		a.pop()
	case key == "enter":
		switch s.m.current() {
		case "new":
			return a.push(newConvertNew(a))
		case "add":
			sc, err := newConvertAdd(a)
			if err != nil {
				a.alert(oneLine(err.Error()))
				return nil
			}
			return a.push(sc)
		case "back":
			a.pop()
		}
	}
	return nil
}

func (s *convertMenu) view(a *App, cw int) string {
	desc := map[string]string{
		"new":  "Genera el script con las tablas y sus datos para una base MySQL vacía.",
		"add":  "Genera solo los INSERT para sumar registros a tablas de una base MySQL existente.",
		"back": "",
	}[s.m.current()]
	return strings.Join([]string{
		sMuted.Render("¿Qué querés hacer con los datos?"),
		"",
		s.m.view(cw),
		"",
		sMuted.Render(clip(desc, cw)),
	}, "\n")
}

// ---------------------------------------------------------------------------
// Vista del script generado (con scroll) y guardado
// ---------------------------------------------------------------------------

// scriptReady llega cuando termina de generarse la vista previa de un script.
type scriptReady struct {
	id        int
	text      string
	truncated bool
	err       error
	name      string                  // nombre de archivo sugerido
	save      func(path string) error // escribe el script COMPLETO en un archivo
}

type saveDone struct {
	id   int
	path string
	err  error
}

type scriptScreen struct {
	id        int
	lines     []string
	truncated bool
	name      string
	save      func(path string) error
	off       int
	btn       int
	saving    bool
}

const scriptRows = 12

func newScriptScreen(m scriptReady) *scriptScreen {
	return &scriptScreen{
		id: nextID(), lines: strings.Split(strings.TrimSuffix(m.text, "\n"), "\n"),
		truncated: m.truncated, name: m.name, save: m.save,
	}
}

func (s *scriptScreen) crumb() string { return "Script SQL generado" }
func (s *scriptScreen) help() string {
	return "↑/↓ desplazar · ←/→ botón · Enter elegir · Esc volver"
}

func (s *scriptScreen) update(a *App, msg tea.Msg) tea.Cmd {
	if d, ok := msg.(saveDone); ok {
		if d.id == s.id {
			s.saving = false
			if d.err != nil {
				a.alert("No se pudo guardar: " + oneLine(d.err.Error()))
			} else {
				a.say("Guardado: " + d.path)
			}
		}
		return nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	maxOff := max(0, len(s.lines)-scriptRows)
	switch key := k.String(); {
	case isUp(key):
		s.off = max(0, s.off-1)
	case isDown(key):
		s.off = min(maxOff, s.off+1)
	case key == "pgup":
		s.off = max(0, s.off-scriptRows)
	case key == "pgdown":
		s.off = min(maxOff, s.off+scriptRows)
	case key == "left" || key == "h":
		s.btn = 0
	case key == "right" || key == "l":
		s.btn = 1
	case key == "esc":
		a.pop()
	case key == "enter":
		if s.btn == 1 {
			a.pop()
			return nil
		}
		if s.saving {
			return nil
		}
		s.saving = true
		path := filepath.Join(a.saveDir(), s.name)
		id, save := s.id, s.save
		return func() tea.Msg {
			err := save(path)
			if err != nil {
				os.Remove(path)
			}
			return saveDone{id, path, err}
		}
	}
	return nil
}

func (s *scriptScreen) view(a *App, cw int) string {
	end := min(len(s.lines), s.off+scriptRows)
	body := make([]string, 0, scriptRows)
	for _, l := range s.lines[s.off:end] {
		body = append(body, sField.Render(padRight(" "+clip(l, cw-2), cw)))
	}
	for len(body) < scriptRows {
		body = append(body, sField.Render(padRight("", cw)))
	}
	pos := fmt.Sprintf("%d líneas · mostrando %d–%d", len(s.lines), s.off+1, end)
	if s.truncated {
		pos = fmt.Sprintf("Vista previa (%d líneas): el script completo es más largo, guardalo para tenerlo entero", len(s.lines))
	}
	save := "Guardar .sql"
	if s.saving {
		save = "Guardando…"
	}
	return strings.Join([]string{
		sMuted.Render(clip(pos, cw)),
		strings.Join(body, "\n"),
		"",
		buttons([]string{save, "Volver"}, s.btn, true),
	}, "\n")
}

// ---------------------------------------------------------------------------
// Barra de progreso
// ---------------------------------------------------------------------------

// progressBar dibuja una barra con degradé azul → dorado.
func progressBar(pct float64, w int) string {
	filled := int(pct*float64(w) + 0.5)
	var b strings.Builder
	for i := 0; i < w; i++ {
		if i < filled {
			t := float64(i) / float64(max(1, w-1))
			c := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
				lerp(0x1b, 0xc8, t), lerp(0x44, 0xa2, t), lerp(0x76, 0x4a, t)))
			b.WriteString(lipgloss.NewStyle().Foreground(c).Render("█"))
		} else {
			b.WriteString(sDisabled.Render("░"))
		}
	}
	return b.String() + sMuted.Render(fmt.Sprintf(" %3d%%", int(pct*100+0.5)))
}

func lerp(a, b int, t float64) int { return a + int(float64(b-a)*t) }
