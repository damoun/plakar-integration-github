package main

import (
	"os"

	"github.com/PlakarKorp/go-kloset-sdk"
	connector "github.com/damoun/plakar-integration-github"
)

func main() {
	sdk.EntrypointExporter(os.Args, connector.NewExporter)
}
