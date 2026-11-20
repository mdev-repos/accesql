package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	maxColWidth = 32  // ancho máximo de una columna en pantalla
	sampleRows  = 200 // filas que se miran para calcular el ancho de cada columna
)

// grid es una tabla de resultados con scroll vertical y horizontal. Todo
// su estado es el cursor y los desplazamientos; el contenido es de solo lectura.
type grid struct {
	cols   []string
	rows   [][]string
	widths []int

	cur    int // fila bajo el cursor
	rowOff int // primera fila visible
	colOff int // primera columna visible
}

// newGrid arma la grilla a partir de columnas y valores crudos.
func newGrid(cols []string, rows [][]any) *grid {
	g := &grid{cols: cols, rows: make([][]string, len(rows)), widths: make([]int, len(cols))}
	for i, c := range cols {
		g.widths[i] = lipgloss.Width(c)
	}
	for n, row := range rows {
		out := make([]string, len(row))
		for i, v := range row {
			out[i] = cellText(v)
			if n < sampleRows {
				if w := lipgloss.Width(out[i]); w > g.widths[i] {
					g.widths[i] = w
				}
			}
		}
		g.rows[n] = out
	}
	for i := range g.widths {
		g.widths[i] = min(g.widths[i], maxColWidth)
	}
	return g
}

// cellText convierte un valor a texto de una sola línea para mostrar.
func cellText(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return fmt.Sprintf("<%d bytes>", len(x))
	case string:
		s = x
	default:
		s = fmt.Sprint(x)
	}
	// Los saltos de línea y tabulaciones romperían el diseño de la pantalla.
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
}

// move desplaza el cursor y las columnas visibles. page es el alto visible,
// para que el cursor nunca quede fuera de la ventana.
func (g *grid) move(dRow, dCol, page int) {
	if len(g.rows) > 0 {
		g.cur = max(0, min(len(g.rows)-1, g.cur+dRow))
	}
	g.colOff = max(0, min(len(g.cols)-1, g.colOff+dCol))
	g.keepVisible(page)
}

func (g *grid) keepVisible(page int) {
	page = max(1, page)
	if g.cur < g.rowOff {
		g.rowOff = g.cur
	}
	if g.cur >= g.rowOff+page {
		g.rowOff = g.cur - page + 1
	}
	g.rowOff = max(0, g.rowOff)
}

// render dibuja la grilla en exactamente h líneas de w columnas de ancho.
func (g *grid) render(w, h int) string {
	if h < 1 || w < 1 {
		return ""
	}
	page := h - 1 // una línea es el encabezado
	g.keepVisible(page)

	lines := make([]string, 0, h)
	lines = append(lines, bold.Render(fitLine(g.line(g.cols, w), w)))
	for i := 0; i < page; i++ {
		n := g.rowOff + i
		if n >= len(g.rows) {
			lines = append(lines, strings.Repeat(" ", w))
			continue
		}
		l := fitLine(g.line(g.rows[n], w), w)
		if n == g.cur {
			l = selected.Render(l)
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// line arma el texto de una fila (o del encabezado) desde la primera columna
// visible, hasta llenar el ancho disponible.
func (g *grid) line(cells []string, w int) string {
	var b strings.Builder
	x := 0
	for c := g.colOff; c < len(g.cols) && x < w; c++ {
		cw := min(g.widths[c], w-x)
		cell := ""
		if c < len(cells) {
			cell = cells[c]
		}
		b.WriteString(padTo(ansi.Truncate(cell, cw, "…"), cw))
		x += cw
		if x < w {
			b.WriteString(" │ ")
			x += 3
		}
	}
	return b.String()
}

// padTo rellena con espacios hasta el ancho w (medido en celdas de pantalla).
func padTo(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// fitLine deja la línea exactamente en w celdas: recorta o rellena.
func fitLine(s string, w int) string {
	return padTo(ansi.Truncate(s, w, ""), w)
}
