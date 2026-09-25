package common

import (
	"os"
	"time"
)

const spinnerTick = 150 * time.Millisecond

// Reuses progressPrinter so ad-hoc spinners share the same animation and
// terminal-detection behavior as snapd change tracking.
func StartProgressSpinner(prefix string) (stop func()) {
	p := newProgressPrinter(os.Stdout)
	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(spinnerTick)
		defer ticker.Stop()

		p.Spin(prefix)
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				p.Spin(prefix)
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
		p.Finished()
	}
}
