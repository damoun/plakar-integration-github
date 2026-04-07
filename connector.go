package integration_github

import (
	"github.com/PlakarKorp/kloset/location"
	"github.com/PlakarKorp/kloset/snapshot/exporter"
	"github.com/PlakarKorp/kloset/snapshot/importer"
)

func init() {
	if err := importer.Register("github", location.Flags(0), NewImporter); err != nil {
		panic(err)
	}
	if err := exporter.Register("github", location.Flags(0), NewExporter); err != nil {
		panic(err)
	}
}
