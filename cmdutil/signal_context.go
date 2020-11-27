package cmdutil

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// SignalContext returns a context that cancels on given syscall signals.
func SignalContext(ctx context.Context) (context.Context, context.CancelFunc) {

	ctx, cancel := context.WithCancel(ctx)

	ch := make(chan os.Signal)
	signal.Notify(ch, []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}...)

	go func() {
		select {
		case sig := <-ch:
			fmt.Println("Closing with received signal.", sig)
		case <-ctx.Done():
		}
		cancel()
	}()

	return ctx, cancel
}
