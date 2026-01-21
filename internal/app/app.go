package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"
)

type Hooks struct {
	Init  any
	Start any
	Stop  any
}

// Module is the single, explicit lifecycle unit.
//
//   - Provide registers DI providers (optional; return nil if none).
//   - Hooks returns optional hook functions; dependencies are expressed as
//     parameters and injected by the container.
//
// Hook function shapes:
//
//	func(context.Context, ...deps) error
//	func(...deps) error
//	func(context.Context, ...deps)
//	func(...deps)
type Module interface {
	Name() string
	Depends() []string
	Provide(r Registry) error
	Hooks() Hooks
}

type App struct {
	container       *container
	shutdownTimeout time.Duration
	started         bool
	regErr          error
	modules         map[string]Module
	moduleList      []Module
	order           []string
	startedOrder    []string
}

func New() *App {
	return &App{container: newContainer("root"), shutdownTimeout: 5 * time.Second, modules: make(map[string]Module)}
}

func (a *App) With(mod Module) *App {
	if a.regErr != nil {
		return a
	}
	if a.container == nil {
		a.container = newContainer("root")
	}
	a.regErr = a.register(mod)
	if a.regErr == nil {
		a.moduleList = append(a.moduleList, mod)
		a.ensureDeps(mod, map[string]bool{})
	}
	return a
}

func (a *App) WithShutdownTimeout(d time.Duration) *App {
	if d > 0 {
		a.shutdownTimeout = d
	}
	return a
}

func (a *App) Start(ctx context.Context) error {
	if a.regErr != nil {
		return a.regErr
	}
	if a.container == nil {
		a.container = newContainer("root")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = withContainerInContext(ctx, a.container)

	// Register providers before hooks.
	for _, mod := range a.moduleList {
		if err := mod.Provide(a.container); err != nil {
			return fmt.Errorf("provide module %s: %w", mod.Name(), err)
		}
	}

	order, err := a.resolveOrder()
	if err != nil {
		return err
	}

	// Init hooks.
	for _, name := range order {
		h := a.modules[name].Hooks()
		if h.Init == nil {
			continue
		}
		if err := a.container.Invoke(ctx, h.Init); err != nil {
			return fmt.Errorf("init module %s: %w", name, err)
		}
	}

	// Start hooks.
	a.startedOrder = a.startedOrder[:0]
	for _, name := range order {
		h := a.modules[name].Hooks()
		if h.Start == nil {
			a.startedOrder = append(a.startedOrder, name)
			continue
		}
		if err := a.container.Invoke(ctx, h.Start); err != nil {
			_ = a.stopHooks(ctx)
			return fmt.Errorf("start module %s: %w", name, err)
		}
		a.startedOrder = append(a.startedOrder, name)
	}
	a.started = true
	return nil
}

func (a *App) Stop(ctx context.Context) error {
	if !a.started {
		return nil
	}
	stopCtx := ctx
	cancel := func() {}
	if stopCtx == nil {
		stopCtx, cancel = context.WithTimeout(context.Background(), a.shutdownTimeout)
	} else {
		stopCtx, cancel = context.WithTimeout(stopCtx, a.shutdownTimeout)
	}
	defer cancel()
	if stopCtx == nil {
		stopCtx = context.Background()
	}
	stopCtx = withContainerInContext(stopCtx, a.container)
	err := a.stopHooks(stopCtx)
	_ = a.container.Shutdown(stopCtx)
	a.started = false
	return err
}

// RunWith starts the app, executes fn, then stops the app.
func (a *App) RunWith(ctx context.Context, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = withContainerInContext(ctx, a.container)
	if err := a.Start(ctx); err != nil {
		_ = a.Stop(context.Background())
		return err
	}
	defer func() { _ = a.Stop(context.Background()) }()
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

// RunUntilSignal starts the app and blocks until ctx cancellation or SIGINT/SIGTERM.
func (a *App) RunUntilSignal(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = withContainerInContext(ctx, a.container)
	if err := a.Start(ctx); err != nil {
		_ = a.Stop(context.Background())
		return err
	}
	defer func() { _ = a.Stop(context.Background()) }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-quit:
		return nil
	}
}

func (a *App) register(mod Module) error {
	if mod == nil {
		return fmt.Errorf("module is nil")
	}
	name := mod.Name()
	if name == "" {
		return fmt.Errorf("module name is empty")
	}
	if _, exists := a.modules[name]; exists {
		return fmt.Errorf("duplicate module name: %s", name)
	}
	a.modules[name] = mod
	a.order = nil
	return nil
}

func (a *App) has(name string) bool {
	_, ok := a.modules[name]
	return ok
}

func (a *App) resolveOrder() ([]string, error) {
	if a.order != nil {
		return a.order, nil
	}

	inDegree := make(map[string]int, len(a.modules))
	deps := make(map[string][]string, len(a.modules))
	reverse := make(map[string][]string, len(a.modules))

	for name, mod := range a.modules {
		inDegree[name] = 0
		deps[name] = append([]string(nil), mod.Depends()...)
	}

	for name := range a.modules {
		for _, dep := range deps[name] {
			if _, ok := a.modules[dep]; !ok {
				return nil, fmt.Errorf("module %s depends on missing module %s", name, dep)
			}
			inDegree[name]++
			reverse[dep] = append(reverse[dep], name)
		}
	}

	q := make([]string, 0, len(a.modules))
	for name, d := range inDegree {
		if d == 0 {
			q = append(q, name)
		}
	}
	sort.Strings(q)

	order := make([]string, 0, len(a.modules))
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		order = append(order, n)
		for _, child := range reverse[n] {
			inDegree[child]--
			if inDegree[child] == 0 {
				q = append(q, child)
			}
		}
		sort.Strings(q)
	}

	if len(order) != len(a.modules) {
		remaining := make([]string, 0)
		for name, d := range inDegree {
			if d != 0 {
				remaining = append(remaining, name)
			}
		}
		sort.Strings(remaining)
		return nil, fmt.Errorf("dependency cycle detected among: %v", remaining)
	}

	a.order = order
	return order, nil
}

func (a *App) stopHooks(ctx context.Context) error {
	var firstErr error
	for i := len(a.startedOrder) - 1; i >= 0; i-- {
		name := a.startedOrder[i]
		h := a.modules[name].Hooks()
		if h.Stop == nil {
			continue
		}
		if err := a.container.Invoke(ctx, h.Stop); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stop module %s: %w", name, err)
		}
	}
	a.startedOrder = a.startedOrder[:0]
	return firstErr
}
