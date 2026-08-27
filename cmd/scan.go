package cmd

import (
	"context"
	"io"
)

// runScan is the whole pipeline: connect, collect, detect, score, render.
// It is filled in as each stage lands.
func runScan(ctx context.Context, o *Options, out io.Writer) error {
	return errNotImplemented
}
