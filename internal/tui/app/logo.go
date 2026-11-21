package app

import (
	"math"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// El logo (cilindro de base de datos con una flecha dorada) se dibuja como
// "píxeles": cada carácter "▀" muestra DOS píxeles apilados, el de arriba con
// el color de texto y el de abajo con el color de fondo. Así la imagen queda
// con proporciones cuadradas y a color en cualquier terminal moderna.

const (
	logoW = 30 // píxeles de ancho = columnas
	logoH = 16 // píxeles de alto  = 8 filas de texto
)

const (
	cTop   = "#7fa8dc" // tapa del cilindro
	cLight = "#4a86cf" // cuerpo, lado iluminado
	cMid   = "#2f6db5" // cuerpo
	cDark  = "#1f4f8a" // cuerpo, lado en sombra
	cGold  = "#d9b155" // bandas y flecha
)

// logoPixels calcula el color de cada píxel con geometría simple (elipses).
// Una cadena vacía significa "transparente".
func logoPixels() [logoH][logoW]string {
	var p [logoH][logoW]string
	const (
		cx, rx = 7.5, 7.0
		ry     = 2.6
		topY   = 2.8  // centro de la elipse superior
		botY   = 12.8 // centro de la elipse inferior
	)
	bands := []float64{6.8, 10.8}

	for y := 0; y < logoH; y++ {
		for x := 0; x < logoW; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5

			// Cilindro
			dx := (px - cx) / rx
			if math.Abs(dx) <= 1 {
				curve := ry * math.Sqrt(1-dx*dx)
				inTop := math.Abs(py-topY) <= curve
				inBody := py >= topY && py <= botY+curve
				switch {
				case inTop:
					p[y][x] = cTop
				case inBody:
					switch {
					case dx < -0.35:
						p[y][x] = cLight
					case dx < 0.45:
						p[y][x] = cMid
					default:
						p[y][x] = cDark
					}
					for _, b := range bands {
						if math.Abs(py-(b+curve)) <= 0.55 {
							p[y][x] = cGold
						}
					}
				}
			}

			// Flecha hacia la derecha
			if px >= 16.5 && px <= 24.5 && math.Abs(py-8) <= 1.0 {
				p[y][x] = cGold
			}
			if px >= 22 && px <= 29.5 && math.Abs(py-8) <= (29.8-px)*0.62 {
				p[y][x] = cGold
			}
		}
	}
	return p
}

var (
	logoOnce sync.Once
	logoText string
)

// logo devuelve el logo ya dibujado (8 filas × 30 columnas). Se calcula una
// sola vez, cuando ya se conoce el perfil de color de la terminal.
func logo() string {
	logoOnce.Do(func() {
		pix := logoPixels()
		lines := make([]string, 0, logoH/2)
		for r := 0; r < logoH/2; r++ {
			var b strings.Builder
			for x := 0; x < logoW; x++ {
				top, bot := pix[2*r][x], pix[2*r+1][x]
				switch {
				case top == "" && bot == "":
					b.WriteByte(' ')
				case top != "" && bot != "":
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(top)).Background(lipgloss.Color(bot)).Render("▀"))
				case top != "":
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(top)).Render("▀"))
				default:
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(bot)).Render("▄"))
				}
			}
			lines = append(lines, b.String())
		}
		logoText = strings.Join(lines, "\n")
	})
	return logoText
}
