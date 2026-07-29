//go:build darwin && amd64

package rg

import _ "embed"

const isWindows = false

//go:embed rg-darwin-amd64
var binary []byte
