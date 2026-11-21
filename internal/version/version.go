// Package version guarda la versión de Accesql. Se puede fijar al compilar:
//
//	go build -ldflags "-X accesql/internal/version.Version=0.2.0" ./cmd/accesql
package version

// Version es la versión mostrada en las interfaces y en los scripts generados.
var Version = "0.1.0"
