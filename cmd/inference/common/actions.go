package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/canonical/inference/internal/snapd"
	"github.com/mattn/go-isatty"
)

const (
	pollInterval = 500 * time.Millisecond
	abortTimeout = 10 * time.Second
)

func NewProgressPrinter(w io.Writer) (progress func(string), finish func()) {
	if w == nil {
		w = io.Discard
	}
	file, ok := w.(*os.File)
	if !ok || !isatty.IsTerminal(file.Fd()) {
		return func(message string) {
			fmt.Fprintln(w, message)
		}, func() {}
	}

	var lineOpen bool
	progress = func(message string) {
		fmt.Fprintf(file, "\r\x1b[K%s", message)
		lineOpen = true
	}
	finish = func() {
		if lineOpen {
			fmt.Fprint(file, "\r\x1b[K")
			lineOpen = false
		}
	}
	return progress, finish
}

func InstallSnap(ctx context.Context, cliCtx *Context, client *snapd.Client, name string) error {
	err := runInstall(ctx, client, name, cliCtx.Stdout)
	switch {
	case err == nil:
		_, err = fmt.Fprintf(cliCtx.Stdout, "Installed %s\n", name)
		return err
	case errors.Is(err, snapd.ErrAlreadyInstalled):
		_, err = fmt.Fprintf(cliCtx.Stdout, "%q is already installed\n", name)
		return err
	case err == context.Canceled:
		return errors.New("installation cancelled")
	case errors.Is(err, snapd.ErrAccessDenied):
		return fmt.Errorf("access denied. Try again using sudo")
	default:
		return fmt.Errorf("installing %s: %w", name, err)
	}
}

func runInstall(ctx context.Context, client *snapd.Client, name string, w io.Writer) error {
	progress, finish := NewProgressPrinter(w)
	defer finish()

	changeID, err := client.Install(ctx, name)
	if errors.Is(err, snapd.ErrChangeConflict) {
		if waitErr := waitForConflictingChange(ctx, client, name, progress); waitErr != nil {
			return waitErr
		}
		changeID, err = client.Install(ctx, name)
	}
	if err != nil {
		return err
	}
	if changeID == "" {
		return nil
	}
	return waitForChangeOrAbort(ctx, client, changeID, progress)
}

func RemoveSnap(ctx context.Context, cliCtx *Context, client *snapd.Client, name string) error {
	err := runRemove(ctx, client, name, cliCtx.Stdout)
	switch {
	case err == nil:
		_, err = fmt.Fprintf(cliCtx.Stdout, "Removed %s\n", name)
		return err
	case errors.Is(err, snapd.ErrNotInstalled):
		_, err = fmt.Fprintf(cliCtx.Stdout, "%q is not installed\n", name)
		return err
	case errors.Is(err, snapd.ErrAccessDenied):
		return fmt.Errorf("access denied. Try again using sudo")
	default:
		return fmt.Errorf("removing %s: %w", name, err)
	}
}

func runRemove(ctx context.Context, client *snapd.Client, name string, w io.Writer) error {
	progress, finish := NewProgressPrinter(w)
	defer finish()

	changeID, err := client.Remove(ctx, name)
	if errors.Is(err, snapd.ErrChangeConflict) {
		if waitErr := waitForConflictingChange(ctx, client, name, progress); waitErr != nil {
			return waitErr
		}
		changeID, err = client.Remove(ctx, name)
	}
	if err != nil {
		return err
	}
	if changeID == "" {
		return nil
	}
	return waitForChangeOrAbort(ctx, client, changeID, progress)
}

func waitForChangeOrAbort(ctx context.Context, client *snapd.Client, changeID string, progress func(string)) error {
	err := waitForChange(ctx, client, changeID, progress)
	if ctx.Err() == nil || err == nil || !errors.Is(err, ctx.Err()) {
		return err
	}

	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
	defer cancel()
	if abortErr := client.Abort(abortCtx, changeID); abortErr != nil {
		return errors.Join(ctx.Err(), fmt.Errorf("aborting snap change %s: %w", changeID, abortErr))
	}
	return ctx.Err()
}

func waitForChange(ctx context.Context, client *snapd.Client, changeID string, progress func(string)) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var lastMessage string
	for {
		change, err := client.Change(ctx, changeID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if message := changeProgressMessage(change); message != "" && message != lastMessage {
			reportProgress(progress, message)
			lastMessage = message
		}
		if change.Ready {
			if change.Status == "Done" {
				return nil
			}
			if change.Err != "" {
				return fmt.Errorf("%s", change.Err)
			}
			return fmt.Errorf("change failed with status %q", change.Status)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForConflictingChange(ctx context.Context, client *snapd.Client, name string, progress func(string)) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var lastMessage string
	for {
		changes, err := client.ChangesInProgress(ctx, name)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			return nil
		}
		if message := changeProgressMessage(changes[0]); message != "" && message != lastMessage {
			reportProgress(progress, message)
			lastMessage = message
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func reportProgress(progress func(string), message string) {
	if progress != nil {
		progress(message)
	}
}

func changeProgressMessage(change snapd.Change) string {
	message := change.Summary
	// Later concurrent tasks reflect the most recent, specific activity.
	for _, task := range change.Tasks {
		if task.Status != "Doing" || task.Summary == "" {
			continue
		}
		if task.Progress.Total > 1 {
			percent := 100 * float64(task.Progress.Done) / float64(task.Progress.Total)
			message = fmt.Sprintf("%s (%.2f%%)", task.Summary, percent)
			continue
		}
		message = task.Summary
	}
	return message
}
