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
	Config             *config.ConfigReader
	SnapdClient        *snapd.Client
	SnapCatalog        *snapcatalog.Reader
	ShareProvidersPath string
	Verbose            bool
	HTTPHost           string
	HTTPPort           string
}
