package plakar_github

import (
	"github.com/PlakarKorp/kloset/connectors/importer"
)

func init() {
	importer.Register("github", 0, NewImporter)
}
