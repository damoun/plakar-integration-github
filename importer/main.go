package main

import (
	"os"

	sdk "github.com/PlakarKorp/go-kloset-sdk"
	connector "github.com/damoun/plakar-integration-github"
)

func main() {
	sdk.EntrypointImporter(os.Args, connector.NewImporter)
}
