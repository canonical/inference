package common

import (
	"io"

	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

type Context struct {
	Stdout             io.Writer
	Stderr             io.Writer
	SnapdClient        *snapd.Client
	SnapCatalog        *snapcatalog.Reader
	ShareProvidersPath string
	Config             *config.ConfigReader
	Verbose            bool
	HTTPHost           string
	HTTPPort           string
}
