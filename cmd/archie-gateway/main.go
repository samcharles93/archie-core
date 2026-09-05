// Command archie-gateway hosts the Gateway Service's gRPC contract.
package main

import (
	"os"

	"github.com/samcharles93/archie-core/internal/app/archied"
)

func main() { os.Exit(archied.RunGateway()) }
