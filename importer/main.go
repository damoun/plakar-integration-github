package main

import (
	"os"

	sdk "github.com/PlakarKorp/go-kloset-sdk"
	connector "github.com/damoun/plakar-github"
)

func main() {
	sdk.EntrypointImporter(os.Args, connector.NewImporter)
}
