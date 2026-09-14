package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/canonical/inference/internal/snapd"
)

var errSnapdControlNotConnected = errors.New(
	`cannot reach snapd. Connect the snapd-control interface`,
)

var errSnapdAccessDenied = errors.New("access denied. Try again using sudo")

func FriendlySnapdError(err error) error {
	switch {
	case errors.Is(err, snapd.ErrAccessDenied):
		return errSnapdAccessDenied
	case errors.Is(err, snapd.ErrSocketUnreachable):
		return errSnapdControlNotConnected
	default:
		return err
	}
}

const (
	pollInterval = 100 * time.Millisecond
	abortTimeout = 10 * time.Second
	// maxTransientPollRetries allows snapd to restart without hiding a
	// persistently unavailable daemon.
	maxTransientPollRetries = 3
	// maxConflictRetries bounds the retry loop so a third party repeatedly
	// starting changes on the same snap cannot livelock us.
	maxConflictRetries = 3
)

func InstallSnap(ctx context.Context, cliCtx *Context, name string) error {
	err := runInstall(ctx, cliCtx.SnapdClient, name, cliCtx.Stdout)
	switch {
	case err == nil:
		_, err = fmt.Fprintf(cliCtx.Stdout, "Installed %s\n", name)
		return err
	case errors.Is(err, snapd.ErrAlreadyInstalled):
		_, err = fmt.Fprintf(cliCtx.Stdout, "%q is already installed\n", name)
		return err
	case errors.Is(err, context.Canceled):
		return cancelledError("installation", err)
	case errors.Is(err, snapd.ErrAccessDenied), errors.Is(err, snapd.ErrSocketUnreachable):
		return FriendlySnapdError(err)
	default:
		return fmt.Errorf("installing %s: %w", name, err)
	}
}

func runInstall(ctx context.Context, client *snapd.Client, name string, w io.Writer) error {
	progress := newProgressPrinter(w)
	defer progress.Finished()

	changeID, err := startWithConflictRetry(ctx, client, name, progress, client.Install)
	if err != nil {
		return err
	}
	return waitForChangeOrAbort(ctx, client, changeID, progress)
}

func RemoveSnap(ctx context.Context, cliCtx *Context, name string) error {
	err := runRemove(ctx, cliCtx.SnapdClient, name, cliCtx.Stdout)
	switch {
	case err == nil:
		_, err = fmt.Fprintf(cliCtx.Stdout, "Removed %s\n", name)
		return err
	case errors.Is(err, snapd.ErrNotInstalled):
		_, err = fmt.Fprintf(cliCtx.Stdout, "%q is not installed\n", name)
		return err
	case errors.Is(err, context.Canceled):
		return cancelledError("removal", err)
	case errors.Is(err, snapd.ErrAccessDenied), errors.Is(err, snapd.ErrSocketUnreachable):
		return FriendlySnapdError(err)
	default:
		return fmt.Errorf("removing %s: %w", name, err)
	}
}

func runRemove(ctx context.Context, client *snapd.Client, name string, w io.Writer) error {
	progress := newProgressPrinter(w)
	defer progress.Finished()

	changeID, err := startWithConflictRetry(ctx, client, name, progress, client.Remove)
	if err != nil {
		return err
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

func waitForChangeOrAbort(ctx context.Context, client *snapd.Client, changeID string, progress *progressPrinter) error {
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

func changeOutcome(change snapd.Change) error {
	if change.Status == "Done" {
		return nil
	}
	if change.Err != "" {
		return errors.New(change.Err)
	}
	return fmt.Errorf("change failed with status %q", change.Status)
}

func waitForChange(ctx context.Context, client *snapd.Client, changeID string, progress *progressPrinter) error {
	var transientFailures int
	for {
		change, err := client.Change(ctx, changeID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, snapd.ErrTransient) && transientFailures < maxTransientPollRetries {
				delay := pollInterval * time.Duration(1<<transientFailures)
				transientFailures++
				progress.Spin("Waiting for snapd to respond")
				if err := waitForNextPoll(ctx, delay); err != nil {
					return err
				}
				continue
			}
			return err
		}
		transientFailures = 0
		if maintenance := change.Maintenance; maintenance != nil {
			switch maintenance.Kind {
			case "system-restart":
				return fmt.Errorf("snapd reports a system restart required for change %s: %s", changeID, maintenance.Message)
			case "daemon-restart":
				progress.Spin(maintenance.Message)
			}
		}
		progress.Update(change)
		if change.Ready {
			return changeOutcome(change)
		}

		if err := waitForNextPoll(ctx, pollInterval); err != nil {
			return err
		}
	}
}

func waitForNextPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func startWithConflictRetry(
	ctx context.Context,
	client *snapd.Client,
	name string,
	progress *progressPrinter,
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

func waitForConflictingChange(ctx context.Context, client *snapd.Client, name string, progress *progressPrinter) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		changes, err := client.ChangesInProgress(ctx, name)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			return nil
		}
		progress.Update(changes[0])

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
