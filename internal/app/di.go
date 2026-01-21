package app

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

type containerCtxKey struct{}

func withContainerInContext(ctx context.Context, c *container) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, containerCtxKey{}, c)
}

func containerFromContext(ctx context.Context) (*container, bool) {
	if ctx == nil {
		return nil, false
	}
	c, ok := ctx.Value(containerCtxKey{}).(*container)
	return c, ok && c != nil
}

// Resolve is an explicit escape hatch for the rare cases where passing
// dependencies via hook parameters is not ergonomic.
//
// Prefer declaring dependencies on hook function parameters instead.
func Resolve[T any](ctx context.Context) (T, error) {
	var zero T
	c, ok := containerFromContext(ctx)
	if !ok {
		return zero, fmt.Errorf("no container in context")
	}

	t := reflect.TypeOf((*T)(nil)).Elem()
	v, err := c.resolve(ctx, key{t: t})
	if err != nil {
		return zero, err
	}
	return v.Interface().(T), nil
}

// ResolveNamed resolves a named registration from ctx.
func ResolveNamed[T any](ctx context.Context, name string) (T, error) {
	var zero T
	c, ok := containerFromContext(ctx)
	if !ok {
		return zero, fmt.Errorf("no container in context")
	}

	t := reflect.TypeOf((*T)(nil)).Elem()
	v, err := c.resolve(ctx, key{t: t, name: name})
	if err != nil {
		return zero, err
	}
	return v.Interface().(T), nil
}

// Registry is the minimal surface a module needs to register providers.
//
// It intentionally does NOT expose resolution, so modules can't turn DI into a
// service locator.
type Registry interface {
	Provide(fn any, opts ...ProvideOption) error
}

type key struct {
	t    reflect.Type
	name string
}

// container is a lightweight DI container with optional named registrations and scopes.
//
// It supports:
// - Singleton instances per-scope.
// - Providers declared as functions with injectable parameters.
// - Scopes for plugin hot-plug (child container inherits parent providers/values).
// - Optional shutdown hooks for providers (for scope disposal).
//
// Provider function shapes:
// - func(...) T
// - func(...) (T, error)
// - func(...) (T, func(context.Context) error)
// - func(...) (T, func(context.Context) error, error)
//
// Injectable parameters:
// - context.Context: passed through from Invoke.
// - any provided type (resolved recursively).
//
// Notes:
// - For multiple implementations, use Named registrations.
// - Cycles are detected during resolution.
type container struct {
	name   string
	parent *container

	mu        sync.Mutex
	values    map[key]reflect.Value
	providers map[key]provider
	resolving map[key]bool
	closers   []func(context.Context) error
	closed    bool
}

type provider struct {
	fn          reflect.Value
	outKey      key
	hasErr      bool
	hasCloser   bool
	closerIndex int
}

func newContainer(name string) *container {
	return &container{
		name:      name,
		values:    make(map[key]reflect.Value),
		providers: make(map[key]provider),
		resolving: make(map[key]bool),
	}
}

type ProvideOption func(*provideOptions)

type provideOptions struct {
	name string
}

func Named(name string) ProvideOption {
	return func(o *provideOptions) { o.name = name }
}

func (c *container) Provide(fn any, opts ...ProvideOption) error {
	if fn == nil {
		return fmt.Errorf("provider is nil")
	}
	var o provideOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	v := reflect.ValueOf(fn)
	t := v.Type()
	if t.Kind() != reflect.Func {
		return fmt.Errorf("provider must be a func, got %s", t.Kind())
	}
	if t.NumOut() < 1 || t.NumOut() > 3 {
		return fmt.Errorf("provider must return 1..3 values, got %d", t.NumOut())
	}

	outT := t.Out(0)
	p := provider{fn: v, outKey: key{t: outT, name: o.name}, closerIndex: -1}

	switch t.NumOut() {
	case 1:
		// T
	case 2:
		// (T, error) or (T, closer)
		if isErrorType(t.Out(1)) {
			p.hasErr = true
		} else if isCloserType(t.Out(1)) {
			p.hasCloser = true
			p.closerIndex = 1
		} else {
			return fmt.Errorf("provider second return must be error or func(context.Context) error")
		}
	case 3:
		// (T, closer, error)
		if !isCloserType(t.Out(1)) || !isErrorType(t.Out(2)) {
			return fmt.Errorf("provider must return (T, func(context.Context) error, error)")
		}
		p.hasCloser = true
		p.closerIndex = 1
		p.hasErr = true
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("container %s is closed", c.name)
	}
	if _, exists := c.providers[p.outKey]; exists {
		return fmt.Errorf("duplicate provider for %s (name=%q)", outT, o.name)
	}
	c.providers[p.outKey] = p
	return nil
}

func (c *container) Invoke(ctx context.Context, fn any) error {
	if fn == nil {
		return fmt.Errorf("invoke fn is nil")
	}
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return fmt.Errorf("invoke target must be func")
	}
	args, err := c.buildArgs(ctx, v.Type())
	if err != nil {
		return err
	}
	outs := v.Call(args)
	if len(outs) == 0 {
		return nil
	}
	last := outs[len(outs)-1]
	if isErrorType(last.Type()) && !last.IsNil() {
		return last.Interface().(error)
	}
	return nil
}

func (c *container) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	closers := append([]func(context.Context) error(nil), c.closers...)
	c.closed = true
	c.mu.Unlock()

	var firstErr error
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closers[i](ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *container) resolve(ctx context.Context, k key) (reflect.Value, error) {
	// Fast path: check local cache.
	c.mu.Lock()
	if v, ok := c.values[k]; ok {
		c.mu.Unlock()
		return v, nil
	}
	if c.resolving[k] {
		c.mu.Unlock()
		return reflect.Value{}, fmt.Errorf("dependency cycle resolving %s (name=%q)", k.t, k.name)
	}
	p, ok := c.providers[k]
	if !ok {
		parent := c.parent
		c.mu.Unlock()
		if parent != nil {
			return parent.resolve(ctx, k)
		}
		return reflect.Value{}, fmt.Errorf("no provider for %s (name=%q)", k.t, k.name)
	}
	c.resolving[k] = true
	c.mu.Unlock()

	// Build args and call provider.
	args, err := c.buildArgs(ctx, p.fn.Type())
	if err != nil {
		c.mu.Lock()
		delete(c.resolving, k)
		c.mu.Unlock()
		return reflect.Value{}, err
	}
	outs := p.fn.Call(args)

	var outV reflect.Value
	var closerFn reflect.Value
	var errV reflect.Value

	switch len(outs) {
	case 1:
		outV = outs[0]
	case 2:
		outV = outs[0]
		if p.hasErr {
			errV = outs[1]
		} else if p.hasCloser {
			closerFn = outs[1]
		}
	case 3:
		outV = outs[0]
		closerFn = outs[1]
		errV = outs[2]
	}

	if p.hasErr && !errV.IsNil() {
		err := errV.Interface().(error)
		c.mu.Lock()
		delete(c.resolving, k)
		c.mu.Unlock()
		return reflect.Value{}, err
	}

	c.mu.Lock()
	delete(c.resolving, k)
	// Another goroutine could have resolved it while we were building; keep singleton semantics.
	if existing, ok := c.values[k]; ok {
		c.mu.Unlock()
		return existing, nil
	}
	c.values[k] = outV
	if p.hasCloser {
		cf := closerFn
		if cf.IsValid() && !cf.IsNil() {
			c.closers = append(c.closers, func(ctx context.Context) error {
				outs := cf.Call([]reflect.Value{reflect.ValueOf(ctx)})
				if len(outs) == 1 && !outs[0].IsNil() {
					return outs[0].Interface().(error)
				}
				return nil
			})
		}
	}
	c.mu.Unlock()

	return outV, nil
}

func (c *container) buildArgs(ctx context.Context, fnType reflect.Type) ([]reflect.Value, error) {
	in := fnType.NumIn()
	args := make([]reflect.Value, 0, in)

	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()

	for i := 0; i < in; i++ {
		paramT := fnType.In(i)
		switch {
		case paramT == ctxType:
			args = append(args, reflect.ValueOf(ctx))
		default:
			v, err := c.resolve(ctx, key{t: paramT})
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
	}

	return args, nil
}

func isErrorType(t reflect.Type) bool {
	errT := reflect.TypeOf((*error)(nil)).Elem()
	return t == errT
}

func isCloserType(t reflect.Type) bool {
	closerT := reflect.TypeOf((func(context.Context) error)(nil))
	return t == closerT
}
