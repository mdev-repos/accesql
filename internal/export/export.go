// Package export convierte el resultado de una consulta a los formatos de
// salida de Accesql (JSON, CSV, INSERT de SQL). Lo comparten la web, la TUI y
// la consola, para que todas generen exactamente lo mismo.
package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"access2mysql/internal/jsonout"
)

// JSON escribe el resultado como una lista de objetos {columna: valor}, con las
// columnas en el orden del SELECT y sangría para que sea legible. Es el mismo
// formato que producen los comandos `query` y `dump`.
func JSON(w io.Writer, res *jsonout.Records) error {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// CSV escribe el resultado como CSV. Con excel=true usa ';' como separador y
// un BOM UTF-8: es lo que Excel en español necesita para abrir el archivo con
// las columnas separadas y los acentos bien.
func CSV(w io.Writer, res *jsonout.Records, excel bool) error {
	if excel {
		if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
			return err
		}
	}
	cw := csv.NewWriter(w)
	if excel {
		cw.Comma = ';'
	}
	if err := cw.Write(res.Columns); err != nil {
		return err
	}
	rec := make([]string, len(res.Columns))
	for _, row := range res.Rows {
		for i, v := range row {
			rec[i] = csvCell(v)
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func csvCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return fmt.Sprintf("<%d bytes>", len(x))
	}
	return neutralizeFormula(fmt.Sprint(v))
}

// neutralizeFormula evita la "inyección de fórmulas" en planillas: una celda
// de texto que empieza con = + - @ se interpretaría como fórmula al abrir el
// CSV en Excel (por ejemplo =HYPERLINK(...) o comandos). Se le antepone una
// comilla simple, que Excel muestra como texto. Los números negativos legítimos
// (-5, -3.2) se dejan intactos.
func neutralizeFormula(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		if isNumber(s) {
			return s
		}
		return "'" + s
	}
	return s
}

func isNumber(s string) bool {
	var f float64
	var rest string
	n, _ := fmt.Sscanf(s, "%g%s", &f, &rest)
	return n == 1
}

// SQLInserts escribe el resultado como sentencias INSERT para la tabla dada.
// Agrupa varias filas por sentencia (hasta ~500 filas o ~256 KB) para que el
// script se ejecute rápido sin pasar el límite de tamaño de paquete de MySQL.
func SQLInserts(w io.Writer, table string, res *jsonout.Records) error {
	ins := NewInsertWriter(w, table, res.Columns)
	for _, row := range res.Rows {
		if err := ins.Add(row); err != nil {
			return err
		}
	}
	return ins.Flush()
}

// Límites de una sentencia INSERT agrupada.
const (
	maxRowsPerInsert  = 500
	maxBytesPerInsert = 256 << 10
)

// InsertWriter arma sentencias INSERT agrupadas sin acumular toda la tabla en
// memoria: se le van pasando las filas con Add y al final se llama a Flush.
type InsertWriter struct {
	w      io.Writer
	head   string // "INSERT INTO `t` (`a`, `b`) VALUES"
	tail   string // lo que va después de la última fila: ";" o "\nON DUPLICATE ..."
	buf    strings.Builder
	rows   int
	failed error
}

// NewInsertWriter crea un InsertWriter. Los nombres van entre comillas
// invertidas, escapadas.
func NewInsertWriter(w io.Writer, table string, columns []string) *InsertWriter {
	return NewCustomInsertWriter(w, "INSERT INTO", table, columns, ";")
}

// NewCustomInsertWriter permite elegir el verbo ("INSERT IGNORE INTO",
// "REPLACE INTO") y el cierre de cada sentencia (por ejemplo, una cláusula
// ON DUPLICATE KEY UPDATE seguida de ";").
func NewCustomInsertWriter(w io.Writer, verb, table string, columns []string, tail string) *InsertWriter {
	cols := make([]string, len(columns))
	for i, c := range columns {
		cols[i] = QuoteIdent(c)
	}
	return &InsertWriter{
		w:    w,
		head: verb + " " + QuoteIdent(table) + " (" + strings.Join(cols, ", ") + ") VALUES",
		tail: tail,
	}
}

// Add agrega una fila; escribe la sentencia cuando llega al límite.
func (iw *InsertWriter) Add(row []any) error {
	if iw.failed != nil {
		return iw.failed
	}
	vals := make([]string, len(row))
	for i, v := range row {
		vals[i] = Literal(v)
	}
	if iw.rows == 0 {
		iw.buf.WriteString(iw.head)
		iw.buf.WriteString("\n  (")
	} else {
		iw.buf.WriteString(",\n  (")
	}
	iw.buf.WriteString(strings.Join(vals, ", "))
	iw.buf.WriteString(")")
	iw.rows++
	if iw.rows >= maxRowsPerInsert || iw.buf.Len() >= maxBytesPerInsert {
		return iw.Flush()
	}
	return nil
}

// Flush escribe la sentencia en curso (si hay filas pendientes).
func (iw *InsertWriter) Flush() error {
	if iw.failed != nil {
		return iw.failed
	}
	if iw.rows == 0 {
		return nil
	}
	iw.buf.WriteString(iw.tail)
	iw.buf.WriteString("\n")
	_, iw.failed = io.WriteString(iw.w, iw.buf.String())
	iw.buf.Reset()
	iw.rows = 0
	return iw.failed
}

// QuoteIdent escribe un nombre de tabla o columna entre comillas invertidas,
// duplicando las que contenga.
func QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// Literal escribe un valor como literal SQL de MySQL.
//
//   - Textos: entre comillas simples, con \ ' y los caracteres de control
//     escapados (así una cadena nunca puede cerrar la comilla ni romper el
//     script, y los saltos de línea no parten la sentencia).
//   - Binarios: hexadecimal (0x...).
//   - Números (incluido json.Number, que conserva los decimales exactos) y
//     booleanos: sin comillas.
//   - Fechas ya formateadas: como texto, que MySQL acepta en columnas DATETIME.
func Literal(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "1"
		}
		return "0"
	case int:
		return strconv.Itoa(x)
	case int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(x)
	case float32:
		return floatLit(float64(x), 32)
	case float64:
		return floatLit(x, 64)
	case []byte:
		if len(x) == 0 {
			return "''"
		}
		return fmt.Sprintf("0x%X", x)
	case time.Time:
		return "'" + x.Format("2006-01-02 15:04:05") + "'"
	case string:
		return quote(x)
	}
	return quote(fmt.Sprint(v))
}

func floatLit(f float64, bits int) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "NULL" // MySQL no tiene NaN ni infinito
	}
	return strconv.FormatFloat(f, 'g', -1, bits)
}

var sqlEscaper = strings.NewReplacer(
	`\`, `\\`,
	`'`, `''`,
	"\x00", `\0`,
	"\n", `\n`,
	"\r", `\r`,
	"\x1a", `\Z`,
)

func quote(s string) string { return "'" + sqlEscaper.Replace(s) + "'" }
