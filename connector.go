package integration_github

import (
	"github.com/PlakarKorp/kloset/connectors/exporter"
	"github.com/PlakarKorp/kloset/connectors/importer"
	"github.com/PlakarKorp/kloset/location"
)

func init() {
	if err := importer.Register("github", location.Flags(0), NewImporter); err != nil {
		panic(err)
	}
	if err := exporter.Register("github", location.Flags(0), NewExporter); err != nil {
		panic(err)
	}
}
