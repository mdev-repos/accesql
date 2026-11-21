package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Abrir base de datos: explorador de carpetas (simulado)
// ---------------------------------------------------------------------------

type fileScreen struct {
	cwd string
	m   menu
}

func newFileScreen() *fileScreen {
	s := &fileScreen{cwd: "/home/usuario/Datos"}
	s.m.rows = 10
	s.reload()
	return s
}

// reload arma la lista de la carpeta actual: subir, carpetas, archivos y volver.
func (s *fileScreen) reload() {
	node := mockFS[s.cwd]
	var items []item
	if s.cwd != "/" {
		items = append(items, item{id: "up", label: "../  (subir una carpeta)"})
	}
	for _, d := range sortedCopy(node.dirs) {
		items = append(items, item{id: "dir:" + d, label: d + "/"})
	}
	for _, f := range sortedCopy(node.files) {
		if accessFile.MatchString(f) {
			items = append(items, item{id: "file:" + f, label: f, hint: strings.ToUpper(filepath.Ext(f)[1:])})
		}
	}
	items = append(items, item{id: "back", label: "Volver"})
	s.m = menu{rows: s.m.rows}
	s.m.setItems(items)
	// Si hay archivos, el cursor arranca en el primero.
	for i, it := range items {
		if strings.HasPrefix(it.id, "file:") {
			s.m.cur = i
			break
		}
	}
	s.m.keepVisible()
}

func (s *fileScreen) crumb() string { return "Abrir base de datos" }
func (s *fileScreen) help() string  { return "↑/↓ mover · Enter abrir · Esc volver" }

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
	case key == "backspace" && s.cwd != "/":
		s.cwd = parentPath(s.cwd)
		s.reload()
	case key == "enter":
		id := s.m.current()
		switch {
		case id == "back":
			a.pop()
		case id == "up":
			s.cwd = parentPath(s.cwd)
			s.reload()
		case strings.HasPrefix(id, "dir:"):
			s.cwd = joinPath(s.cwd, strings.TrimPrefix(id, "dir:"))
			s.reload()
		case strings.HasPrefix(id, "file:"):
			a.st.filePath = joinPath(s.cwd, strings.TrimPrefix(id, "file:"))
			a.st.imageReady = false // una base nueva exige generar la imagen de nuevo
			a.st.serverAddr = ""
			a.pop()
			a.say("Base abierta: " + baseName(a.st.filePath))
		}
	}
	return nil
}

func (s *fileScreen) view(a *App, cw int) string {
	return strings.Join([]string{
		sMuted.Render("Carpeta: ") + sGoldBold.Render(s.cwd),
		sMuted.Render("Solo se muestran bases de Access (.accdb, .mdb)."),
		"",
		s.m.view(cw),
	}, "\n")
}

// ---------------------------------------------------------------------------
// Generar imagen MySQL: barra de progreso (simulada)
// ---------------------------------------------------------------------------

type genTick struct{ id int }

var genCounter int

type genScreen struct {
	id  int
	idx int
}

func newGenScreen(a *App) *genScreen {
	genCounter++
	return &genScreen{id: genCounter}
}

func (s *genScreen) crumb() string { return "Generar imagen MySQL" }
func (s *genScreen) help() string  { return "Esc cancelar" }

func (s *genScreen) init() tea.Cmd { return s.tick() }

func (s *genScreen) tick() tea.Cmd {
	id := s.id
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return genTick{id} })
}

func (s *genScreen) update(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		if m.String() == "esc" {
			a.pop()
			a.alert("Generación cancelada.")
		}
	case genTick:
		if m.id != s.id {
			return nil
		}
		s.idx++
		if s.idx >= len(mockTables) {
			a.st.imageReady = true
			a.pop()
			a.say("✔ ¡Imagen MySQL generada!")
			return nil
		}
		return s.tick()
	}
	return nil
}

func (s *genScreen) view(a *App, cw int) string {
	n := len(mockTables)
	cur := mockTables[min(s.idx, n-1)].name
	pct := float64(s.idx) / float64(n)
	return strings.Join([]string{
		sMuted.Render("Leyendo ") + sTitle.Render(baseName(a.st.filePath)) + sMuted.Render(" (solo lectura)"),
		"",
		"  " + progressBar(pct, max(10, cw-12)),
		"",
		sMuted.Render(fmt.Sprintf("  Tabla %d de %d: ", min(s.idx+1, n), n)) + sGold.Render(cur),
	}, "\n")
}

// ---------------------------------------------------------------------------
// Abrir interfaz gráfica (simulada)
// ---------------------------------------------------------------------------

type guiScreen struct{ m menu }

func newGUIScreen() *guiScreen {
	s := &guiScreen{}
	s.m.setItems([]item{{id: "back", label: "Volver"}})
	return s
}

func (s *guiScreen) crumb() string { return "Interfaz gráfica" }
func (s *guiScreen) help() string  { return "Enter volver · Esc volver" }

func (s *guiScreen) update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok && (k.String() == "enter" || k.String() == "esc") {
		a.pop()
	}
	return nil
}

func (s *guiScreen) view(a *App, cw int) string {
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colGold).Padding(0, 2)
	return strings.Join([]string{
		sMuted.Render("La interfaz web se abriría en tu navegador en esta dirección:"),
		"",
		box.Render(sGoldBold.Render("http://127.0.0.1:8080/#t=3f9a1c…")),
		"",
		sMuted.Render("Compartiría la misma base ya cargada, sin volver a leer el archivo."),
		sDisabled.Render("(maqueta: no se inició ningún servidor)"),
		"",
		s.m.view(cw),
	}, "\n")
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
			return a.push(newConvertAdd())
		case "back":
			a.pop()
		}
	}
	return nil
}

func (s *convertMenu) view(a *App, cw int) string {
	cur := s.m.current()
	desc := map[string]string{
		"new":  "Genera el script con las tablas y sus datos para una base MySQL vacía.",
		"add":  "Genera solo los INSERT para sumar registros a tablas de una base MySQL existente.",
		"back": "",
	}[cur]
	return strings.Join([]string{
		sMuted.Render("¿Qué querés hacer con los datos?"),
		"",
		s.m.view(cw),
		"",
		sMuted.Render(desc),
	}, "\n")
}

// ---------------------------------------------------------------------------
// Vista del script generado (con scroll)
// ---------------------------------------------------------------------------

type scriptScreen struct {
	lines []string
	off   int
	btn   int
}

const scriptRows = 12

func newScriptScreen(text string) *scriptScreen {
	return &scriptScreen{lines: strings.Split(text, "\n")}
}

func (s *scriptScreen) crumb() string { return "Script SQL generado" }
func (s *scriptScreen) help() string {
	return "↑/↓ desplazar · ←/→ botón · Enter elegir · Esc volver"
}

func (s *scriptScreen) update(a *App, msg tea.Msg) tea.Cmd {
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
		} else {
			a.say("(maqueta) Se guardaría accesql.sql en la carpeta actual.")
		}
	}
	return nil
}

func (s *scriptScreen) view(a *App, cw int) string {
	end := min(len(s.lines), s.off+scriptRows)
	body := make([]string, 0, scriptRows)
	for _, l := range s.lines[s.off:end] {
		body = append(body, sField.Render(padRight(" "+l, cw)))
	}
	for len(body) < scriptRows {
		body = append(body, sField.Render(padRight("", cw)))
	}
	pos := sMuted.Render(fmt.Sprintf("%d líneas · mostrando %d–%d · ejemplo", len(s.lines), s.off+1, end))
	return strings.Join([]string{pos, strings.Join(body, "\n"), "", buttons([]string{"Guardar .sql", "Volver"}, s.btn, true)}, "\n")
}

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
