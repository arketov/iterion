//go:build desktop

package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/arketov/iterion/pkg/alert"
	"github.com/arketov/iterion/pkg/cli"
)

// ServerHost wraps cli.RunStudio so the desktop App can stop and restart
// the studio server when the user switches projects. Stop blocks until
// RunStudio's drain budget elapses or the goroutine returns.
type serverController interface {
	Start(parent context.Context, dir, storeDir string) (string, error)
	Stop()
}

type ServerHost struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{} // closed when the underlying RunStudio goroutine returns

	// alertSink is forwarded into every studio server this host starts as
	// the desktop run-health notification sink. nil disables native
	// desktop notifications (RunStudio still wires the browser toast + dot
	// and the optional webhook). Set once at construction.
	alertSink alert.Sink
}

// NewServerHost returns an unstarted host. alertSink, when non-nil, is
// wired as the desktop alert delivery sink for every embedded studio
// server (gated server-side on ITERION_ALERTS_DESKTOP_ENABLED).
func NewServerHost(alertSink alert.Sink) *ServerHost {
	return &ServerHost{alertSink: alertSink}
}

// Start brings up the studio server on a random port for the given
// project and returns the bound "host:port" once the listener is ready.
// Calling Start while a server is already running is an error — the
// desktop App always calls Stop first.
func (h *ServerHost) Start(parent context.Context, dir, storeDir string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		return "", fmt.Errorf("server already running; call Stop first")
	}

	ctx, cancel := context.WithCancel(parent)
	h.cancel = cancel
	h.done = make(chan struct{})

	addrCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(h.done)
		// Use a noop printer — the desktop has no stdout for the user.
		printer := cli.NewPrinter(cli.OutputJSON)
		opts := cli.StudioOptions{
			Port:                   -1, // random
			Bind:                   "127.0.0.1",
			Dir:                    dir,
			StoreDir:               storeDir,
			MaxConcurrentPipelines: cli.DefaultMaxConcurrentPipelines,
			NoBrowser:              true,
			OnReady: func(addr string) {
				select {
				case addrCh <- addr:
				default:
				}
			},
			OnForceRefresh:   ReloadIterionEnvFile,
			DesktopAlertSink: h.alertSink,
		}
		if err := cli.RunStudio(ctx, opts, printer); err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	// Wait for either a bind or an error or a generous startup timeout.
	select {
	case addr := <-addrCh:
		return addr, nil
	case err := <-errCh:
		h.cancel = nil
		return "", err
	case <-time.After(15 * time.Second):
		cancel()
		<-h.done
		h.cancel = nil
		return "", fmt.Errorf("server failed to bind within 15s")
	}
}

// Stop cancels the current RunStudio context, triggering the 60s drain
// budget inside RunStudio. Stop blocks until RunStudio returns or the
// drain budget elapses (whichever first). Calling Stop on a stopped host
// is a no-op.
func (h *ServerHost) Stop() {
	h.mu.Lock()
	cancel := h.cancel
	done := h.done
	h.cancel = nil
	h.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		// RunStudio's drain is bounded at 60s internally; we add a small
		// safety margin here so a wedged drain doesn't deadlock the desktop.
		select {
		case <-done:
		case <-time.After(65 * time.Second):
		}
	}
}
