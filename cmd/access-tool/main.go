package main

import (
	"os"

	"access2mysql/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// cobra ya imprimió el mensaje de error; solo fijamos el código de salida.
		os.Exit(1)
	}
}
