//go:build windows && amd64

package rg

import _ "embed"

const isWindows = true

//go:embed rg-windows-amd64.exe
var binary []byte
