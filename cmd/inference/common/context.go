package common

import (
	"io"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/canonical/inference/internal/storage"
)

type Context struct {
	Stdout             io.Writer
	Stderr             io.Writer
	SnapdClient        *snapd.Client
	SnapCatalog        *snapcatalog.Reader
	ShareProvidersPath string
	Storage            *storage.Storage
	Verbose            bool
	HTTPHost           string
	HTTPPort           string
}
