package common

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	snapctlenv "github.com/canonical/go-snapctl/env"
	"github.com/canonical/inference/internal/snapd"
)

const modelDiscoveryTimeout = 30 * time.Second

var ErrServiceInactive = errors.New("service is not active")

// TriggerModelDiscovery forces a refresh of the service's models lists.
func TriggerModelDiscovery(ctx context.Context, cliCtx *Context) error {
	snapInstanceName := snapctlenv.SnapInstanceName()
	if snapInstanceName == "" {
		// Fall back to snap name when not inside a snap
		snapInstanceName = InferenceSnapName
	}
	// Best effort: if the service state is unknown, still try to reach it.
	serviceStatus, err := cliCtx.SnapdClient.ServiceStatus(ctx, snapInstanceName, InferenceService)
	if err == nil && serviceStatus != snapd.ServiceStatusActive {
		return ErrServiceInactive
	}

	ctx, cancel := context.WithTimeout(ctx, modelDiscoveryTimeout)
	defer cancel()

	endpoint := "http://" + net.JoinHostPort(cliCtx.HTTPHost, cliCtx.HTTPPort) + "/v1/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("model discovery returned %s", response.Status)
	}
	return nil
}
