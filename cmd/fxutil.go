package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/fx"
)

func runFxUntilSignal(ctx context.Context, app *fx.App, stopTimeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if app == nil {
		return nil
	}
	if err := app.Start(ctx); err != nil {
		_ = stopFx(context.Background(), app, stopTimeout)
		return err
	}
	defer func() { _ = stopFx(context.Background(), app, stopTimeout) }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-quit:
		return nil
	case sig := <-app.Wait():
		if sig.ExitCode != 0 {
			return fmt.Errorf("fx shutdown (exitCode=%d)", sig.ExitCode)
		}
		return nil
	}
}

func runFxWith(ctx context.Context, app *fx.App, stopTimeout time.Duration, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if app == nil {
		return nil
	}
	if err := app.Start(ctx); err != nil {
		_ = stopFx(context.Background(), app, stopTimeout)
		return err
	}
	defer func() { _ = stopFx(context.Background(), app, stopTimeout) }()
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

func stopFx(ctx context.Context, app *fx.App, stopTimeout time.Duration) error {
	if app == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if stopTimeout <= 0 {
		return app.Stop(ctx)
	}
	stopCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	return app.Stop(stopCtx)
}
