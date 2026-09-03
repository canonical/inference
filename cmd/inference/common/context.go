package common

import "io"

type Context struct {
	Stdout io.Writer
	Stderr io.Writer
}
