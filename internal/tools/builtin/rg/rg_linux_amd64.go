//go:build linux && amd64

package rg

import _ "embed"

const isWindows = false

//go:embed rg-linux-amd64
var binary []byte
