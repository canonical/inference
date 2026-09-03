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
	// maxConflictRetries bounds the retry loop so a third party repeatedly
	// starting changes on the same snap cannot livelock us.
	maxConflictRetries = 3
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
	case errors.Is(err, context.Canceled):
		return cancelledError("installation", err)
	case errors.Is(err, snapd.ErrAccessDenied):
		return fmt.Errorf("access denied. Try again using sudo")
	default:
		return fmt.Errorf("installing %s: %w", name, err)
	}
}

func runInstall(ctx context.Context, client *snapd.Client, name string, w io.Writer) error {
	progress, finish := NewProgressPrinter(w)
	defer finish()

	changeID, err := startWithConflictRetry(ctx, client, name, progress, client.Install)
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
	case errors.Is(err, context.Canceled):
		return cancelledError("removal", err)
	case errors.Is(err, snapd.ErrAccessDenied):
		return fmt.Errorf("access denied. Try again using sudo")
	default:
		return fmt.Errorf("removing %s: %w", name, err)
	}
}

func runRemove(ctx context.Context, client *snapd.Client, name string, w io.Writer) error {
	progress, finish := NewProgressPrinter(w)
	defer finish()

	changeID, err := startWithConflictRetry(ctx, client, name, progress, client.Remove)
	if err != nil {
		return err
	}
	if changeID == "" {
		return nil
	}
	return waitForChangeOrAbort(ctx, client, changeID, progress)
}

// The change may still be running after a failed abort.
type abortError struct {
	changeID string
	err      error
}

func (e *abortError) Error() string {
	return fmt.Sprintf("could not abort snap change %s: %v", e.changeID, e.err)
}

func (e *abortError) Unwrap() error { return e.err }

func cancelledError(action string, err error) error {
	var abortErr *abortError
	if errors.As(err, &abortErr) {
		return fmt.Errorf("%s cancelled: %w", action, abortErr)
	}
	return fmt.Errorf("%s cancelled", action)
}

func waitForChangeOrAbort(ctx context.Context, client *snapd.Client, changeID string, progress func(string)) error {
	err := waitForChange(ctx, client, changeID, progress)
	if ctx.Err() == nil || err == nil || !errors.Is(err, ctx.Err()) {
		return err
	}

	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
	defer cancel()

	// The change may have completed in the same tick the context was cancelled.
	// snapd rejects aborting a ready change, so report its outcome instead.
	if change, changeErr := client.Change(abortCtx, changeID); changeErr == nil && change.Ready {
		return changeOutcome(change)
	}

	if abortErr := client.Abort(abortCtx, changeID); abortErr != nil {
		return errors.Join(ctx.Err(), &abortError{changeID: changeID, err: abortErr})
	}
	return ctx.Err()
}

// changeOutcome converts a ready change into the corresponding result.
func changeOutcome(change snapd.Change) error {
	if change.Status == "Done" {
		return nil
	}
	if change.Err != "" {
		return errors.New(change.Err)
	}
	return fmt.Errorf("change failed with status %q", change.Status)
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
			return changeOutcome(change)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// startWithConflictRetry runs action, waiting out any change already in progress
// on the snap and retrying up to maxConflictRetries times. The final conflict is
// returned once the attempts are exhausted.
func startWithConflictRetry(
	ctx context.Context,
	client *snapd.Client,
	name string,
	progress func(string),
	action func(context.Context, string) (string, error),
) (string, error) {
	for attempt := 0; ; attempt++ {
		changeID, err := action(ctx, name)
		if !errors.Is(err, snapd.ErrChangeConflict) || attempt >= maxConflictRetries {
			return changeID, err
		}
		if waitErr := waitForConflictingChange(ctx, client, name, progress); waitErr != nil {
			return "", fmt.Errorf("waiting for conflicting change on %s: %w", name, waitErr)
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
