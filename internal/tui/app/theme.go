// Package app es la interfaz de terminal principal de Accesql: un menú con
// logo, pantallas internas y botón VOLVER en cada submenú.
//
// ESTADO ACTUAL: MAQUETA. Todo lo que muestra es simulado (no abre archivos ni
// levanta servidores). Sirve para aprobar el aspecto y la navegación antes de
// conectar la funcionalidad real.
package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Paleta: azul en barras y selección, dorado en detalles, grises neutros.
var (
	colBlue  = lipgloss.Color("#2a5a94")
	colNavy  = lipgloss.Color("#1b4476")
	colGold  = lipgloss.Color("#c8a24a")
	colOK    = lipgloss.Color("#3fa564")
	colErr   = lipgloss.Color("#e0574b")
	colWhite = lipgloss.Color("#ffffff")
	colMuted = lipgloss.AdaptiveColor{Light: "#5a6676", Dark: "#8a94a3"}
	colText  = lipgloss.AdaptiveColor{Light: "#1c2633", Dark: "#e8ecf2"}
	colField = lipgloss.AdaptiveColor{Light: "#d9dde3", Dark: "#2b3441"}
)

var (
	sTitle    = lipgloss.NewStyle().Bold(true).Foreground(colText)
	sVersion  = lipgloss.NewStyle().Foreground(colGold).Bold(true)
	sMuted    = lipgloss.NewStyle().Foreground(colMuted)
	sDisabled = lipgloss.NewStyle().Foreground(colMuted).Faint(true)
	sGold     = lipgloss.NewStyle().Foreground(colGold)
	sGoldBold = lipgloss.NewStyle().Foreground(colGold).Bold(true)
	sOK       = lipgloss.NewStyle().Foreground(colOK).Bold(true)
	sErr      = lipgloss.NewStyle().Foreground(colErr).Bold(true)
	sSel      = lipgloss.NewStyle().Background(colBlue).Foreground(colWhite).Bold(true)
	sSelMark  = lipgloss.NewStyle().Background(colBlue).Foreground(colGold).Bold(true)
	sHeader   = lipgloss.NewStyle().Background(colNavy).Foreground(colWhite).Bold(true)
	sBtn      = lipgloss.NewStyle().Foreground(colText).Background(colField).Padding(0, 1)
	sBtnSel   = lipgloss.NewStyle().Foreground(colWhite).Background(colBlue).Bold(true).Padding(0, 1)
	sField    = lipgloss.NewStyle().Foreground(colText).Background(colField)
)

// padRight rellena con espacios hasta w columnas (mide ancho visible, no bytes).
func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// clip recorta a w columnas sin romper secuencias de color.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// row dibuja una fila de menú con el mismo estilo para todas las opciones:
// la seleccionada va con barra azul y marcador dorado, la bloqueada en gris.
func row(label, hint string, sel, disabled bool, width int) string {
	inner := width - 3
	hintW := lipgloss.Width(hint)
	label = clip(label, max(1, inner-hintW-1))
	gap := max(1, inner-lipgloss.Width(label)-hintW)
	switch {
	case sel:
		return sSelMark.Render(" ▸ ") + sSel.Render(padRight(label+strings.Repeat(" ", gap)+hint, inner))
	case disabled:
		return padRight("   "+sDisabled.Render(label)+strings.Repeat(" ", gap)+sDisabled.Render(hint), width)
	default:
		return padRight("   "+label+strings.Repeat(" ", gap)+sMuted.Render(hint), width)
	}
}

// buttons dibuja una fila de botones; el activo va en azul.
func buttons(labels []string, cur int, focused bool) string {
	parts := make([]string, len(labels))
	for i, l := range labels {
		if focused && i == cur {
			parts[i] = sBtnSel.Render("▸ " + l)
		} else {
			parts[i] = sBtn.Render(l)
		}
	}
	return strings.Join(parts, "  ")
}

// check dibuja una casilla: [x] o [ ].
func check(on bool, label string) string {
	if on {
		return "[x] " + label
	}
	return "[ ] " + label
}

// wrap parte un texto en líneas de hasta w columnas (cortando entre palabras) y
// deja como máximo n; si no entra, la última termina en "…".
func wrap(s string, w, n int) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > n {
		lines = append(lines[:n-1], clip(strings.Join(lines[n-1:], " "), w))
	}
	return lines
}
