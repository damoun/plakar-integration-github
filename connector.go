package integration_github

import (
	"github.com/PlakarKorp/kloset/location"
	"github.com/PlakarKorp/kloset/snapshot/importer"
)

func init() {
	importer.Register("github", location.Flags(0), NewImporter)
}
