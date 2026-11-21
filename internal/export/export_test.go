package export

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"accesql/internal/jsonout"
)

func TestLiteral(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "NULL"},
		{json.Number("12.50"), "12.50"},
		{true, "1"},
		{false, "0"},
		{int64(-7), "-7"},
		{uint8(200), "200"},
		{1.5, "1.5"},
		{float32(0.25), "0.25"},
		{math.NaN(), "NULL"},
		{math.Inf(1), "NULL"},
		{[]byte{0xDE, 0xAD}, "0xDEAD"},
		{[]byte{}, "''"},
		{"hola", "'hola'"},
		{"O'Hara", "'O''Hara'"},
		{`a\b`, `'a\\b'`},
		{"línea1\nlínea2\r", `'línea1\nlínea2\r'`},
		{"nul\x00ctrlz\x1a", `'nul\0ctrlz\Z'`},
		{"2006-01-02T15:04:05", "'2006-01-02T15:04:05'"},
	}
	for _, c := range cases {
		if got := Literal(c.in); got != c.want {
			t.Errorf("Literal(%#v) = %s, quiero %s", c.in, got, c.want)
		}
	}
}

// Una cadena maliciosa nunca debe poder cerrar la comilla del literal.
func TestLiteralCannotBreakOut(t *testing.T) {
	evil := "x'); DROP TABLE t; --"
	lit := Literal(evil)
	if lit != `'x''); DROP TABLE t; --'` {
		t.Fatalf("no se escapó bien: %s", lit)
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := QuoteIdent("Order `Details`"); got != "`Order ``Details```" {
		t.Errorf("got %s", got)
	}
}

func TestCSV(t *testing.T) {
	res := &jsonout.Records{
		Columns: []string{"a", "b"},
		Rows: [][]any{
			{"=HYPERLINK(\"x\")", "-5"},
			{"+cmd", nil},
			{"normal, con coma", []byte{1, 2}},
		},
	}
	var plain bytes.Buffer
	if err := CSV(&plain, res, false); err != nil {
		t.Fatal(err)
	}
	out := plain.String()
	for _, want := range []string{"a,b\n", `"'=HYPERLINK(""x"")",-5`, "'+cmd,\n", `"normal, con coma",<2 bytes>`} {
		if !strings.Contains(out, want) {
			t.Errorf("CSV sin %q:\n%s", want, out)
		}
	}
	var excel bytes.Buffer
	if err := CSV(&excel, res, true); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(excel.String(), "\xEF\xBB\xBFa;b\n") {
		t.Errorf("el CSV para Excel debe empezar con BOM y usar ';': %q", excel.String()[:12])
	}
}

func TestJSON(t *testing.T) {
	res := &jsonout.Records{Columns: []string{"z", "a"}, Rows: [][]any{{1, json.Number("2.50")}}}
	var b bytes.Buffer
	if err := JSON(&b, res); err != nil {
		t.Fatal(err)
	}
	var back []map[string]any
	if err := json.Unmarshal(b.Bytes(), &back); err != nil {
		t.Fatalf("no es JSON válido: %v\n%s", err, b.String())
	}
	if len(back) != 1 || back[0]["z"] != float64(1) {
		t.Errorf("contenido inesperado: %v", back)
	}
	out := b.String()
	if strings.Index(out, `"z"`) > strings.Index(out, `"a"`) {
		t.Errorf("se perdió el orden de las columnas:\n%s", out)
	}
	if !strings.Contains(out, "2.50") {
		t.Errorf("el decimal debe conservarse exacto:\n%s", out)
	}
}

func TestSQLInsertsBatches(t *testing.T) {
	res := &jsonout.Records{Columns: []string{"id", "txt"}}
	for i := 0; i < 1200; i++ {
		res.Rows = append(res.Rows, []any{i, "fila"})
	}
	var b bytes.Buffer
	if err := SQLInserts(&b, "mi tabla", res); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if n := strings.Count(out, "INSERT INTO `mi tabla` (`id`, `txt`) VALUES"); n != 3 {
		t.Errorf("1200 filas deberían salir en 3 sentencias (500+500+200), hay %d", n)
	}
	if n := strings.Count(out, "\n  ("); n != 1200 {
		t.Errorf("deberían estar las 1200 filas, hay %d", n)
	}
	if !strings.HasSuffix(out, ");\n") {
		t.Errorf("la última sentencia debe cerrar con ;")
	}
}

func TestSQLInsertsEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := SQLInserts(&b, "t", &jsonout.Records{Columns: []string{"a"}}); err != nil || b.Len() != 0 {
		t.Errorf("sin filas no debe escribir nada (err=%v, %q)", err, b.String())
	}
}
