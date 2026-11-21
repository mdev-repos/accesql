package app

import "strings"

// item es una opción de una lista. Las bloqueadas se ven en gris y el cursor
// las saltea.
type item struct {
	id       string
	label    string
	hint     string
	disabled bool
}

// menu es una lista de opciones con cursor y desplazamiento (scroll).
type menu struct {
	items []item
	cur   int
	top   int // primera fila visible
	rows  int // filas visibles (0 = todas)
}

// setItems reemplaza las opciones conservando el cursor si todavía es válido.
func (m *menu) setItems(items []item) {
	m.items = items
	if m.cur >= len(items) {
		m.cur = max(0, len(items)-1)
	}
	if m.cur < len(items) && items[m.cur].disabled {
		m.move(1)
	}
	m.keepVisible()
}

// move mueve el cursor d posiciones, salteando las bloqueadas y dando la vuelta
// al llegar a un extremo.
func (m *menu) move(d int) {
	n := len(m.items)
	for i := 0; i < n; i++ {
		m.cur = (m.cur + d + n) % n
		if !m.items[m.cur].disabled {
			break
		}
	}
	m.keepVisible()
}

func (m *menu) keepVisible() {
	if m.rows <= 0 {
		return
	}
	if m.cur < m.top {
		m.top = m.cur
	}
	if m.cur >= m.top+m.rows {
		m.top = m.cur - m.rows + 1
	}
	if m.top < 0 {
		m.top = 0
	}
}

// current devuelve la opción bajo el cursor ("" si no hay).
func (m *menu) current() string {
	if m.cur < 0 || m.cur >= len(m.items) {
		return ""
	}
	return m.items[m.cur].id
}

// view dibuja las filas visibles, todas del mismo ancho.
func (m *menu) view(width int) string {
	end := len(m.items)
	start := 0
	if m.rows > 0 {
		start = m.top
		end = min(len(m.items), m.top+m.rows)
	}
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		it := m.items[i]
		lines = append(lines, row(it.label, it.hint, i == m.cur && !it.disabled, it.disabled, width))
	}
	return strings.Join(lines, "\n")
}

// isUp / isDown agrupan las teclas de navegación vertical.
func isUp(k string) bool   { return k == "up" || k == "k" }
func isDown(k string) bool { return k == "down" || k == "j" }
