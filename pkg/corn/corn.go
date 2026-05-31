package corn

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Func 是 corn 调度器执行的任务函数。
// 调度器会为每次执行传入可取消的 context，便于任务在重载或关闭时尽快退出。
type Func func(ctx context.Context)

type job struct {
	cancel context.CancelFunc
}

type schedule interface {
	Next(time.Time) time.Time
}

type everySchedule struct {
	interval time.Duration
}

func (s everySchedule) Next(t time.Time) time.Time {
	return t.Add(s.interval)
}

type cronSchedule struct {
	seconds map[int]struct{}
	minutes map[int]struct{}
	hours   map[int]struct{}
	dom     map[int]struct{}
	months  map[int]struct{}
	dow     map[int]struct{}
}

func (s cronSchedule) Next(t time.Time) time.Time {
	next := t.Truncate(time.Second).Add(time.Second)
	limit := next.Add(366 * 24 * time.Hour)
	for next.Before(limit) {
		if s.match(next) {
			return next
		}
		next = next.Add(time.Second)
	}
	return time.Time{}
}

func (s cronSchedule) match(t time.Time) bool {
	_, okSecond := s.seconds[t.Second()]
	_, okMinute := s.minutes[t.Minute()]
	_, okHour := s.hours[t.Hour()]
	_, okDay := s.dom[t.Day()]
	_, okMonth := s.months[int(t.Month())]
	_, okWeek := s.dow[int(t.Weekday())]
	return okSecond && okMinute && okHour && okDay && okMonth && okWeek
}

type Manager struct {
	mu   sync.Mutex
	jobs map[string]job
}

var defaultManager = NewManager()

func NewManager() *Manager {
	return &Manager{jobs: make(map[string]job)}
}

// AddFunc 按 cron 表达式注册一个任务，fn 会在独立 goroutine 中执行。
// 支持 5 字段、6 字段 cron 表达式，以及 @every 1m 这类固定间隔表达式。
func AddFunc(name string, spec string, fn func()) error {
	return AddContextFunc(name, spec, false, func(context.Context) { fn() })
}

// AddContextFunc 按 cron 表达式注册一个任务，支持传递带 context 的 func。
func AddContextFunc(name string, spec string, runImmediately bool, fn Func) error {
	return defaultManager.AddContextFunc(name, spec, runImmediately, fn)
}

func Every(duration time.Duration) string {
	return "@every " + duration.String()
}

func Remove(name string) {
	defaultManager.Remove(name)
}

func RemovePrefix(prefix string) {
	defaultManager.RemovePrefix(prefix)
}

func StopAll() {
	defaultManager.StopAll()
}

func (m *Manager) AddFunc(name string, spec string, fn func()) error {
	return m.AddContextFunc(name, spec, false, func(context.Context) { fn() })
}

func (m *Manager) AddContextFunc(name string, spec string, runImmediately bool, fn Func) error {
	if name == "" {
		return fmt.Errorf("corn job name is empty")
	}
	s, err := Parse(spec)
	if err != nil {
		return fmt.Errorf("corn job %q spec is invalid: %w", name, err)
	}
	if fn == nil {
		return fmt.Errorf("corn job %q func is nil", name)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.replace(name, cancel)

	go m.run(ctx, name, s, runImmediately, fn)
	return nil
}

func (m *Manager) Remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.jobs[name]; ok {
		old.cancel()
		delete(m.jobs, name)
	}
}

func (m *Manager) RemovePrefix(prefix string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, old := range m.jobs {
		if strings.HasPrefix(name, prefix) {
			old.cancel()
			delete(m.jobs, name)
		}
	}
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, old := range m.jobs {
		old.cancel()
		delete(m.jobs, name)
	}
}

func (m *Manager) replace(name string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.jobs[name]; ok {
		old.cancel()
	}
	m.jobs[name] = job{cancel: cancel}
}

func (m *Manager) run(ctx context.Context, name string, s schedule, runImmediately bool, fn Func) {
	if runImmediately {
		go safeRun(ctx, name, fn)
	}

	nextTick := s.Next(time.Now())
	if nextTick.IsZero() {
		log.Printf("corn job %s has no next run time", name)
		return
	}
	timer := time.NewTimer(time.Until(nextTick))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			go safeRun(ctx, name, fn)
			nextTick = s.Next(nextTick)
			if nextTick.IsZero() {
				return
			}
			resetTimer(timer, time.Until(nextTick))
		}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if duration < 0 {
		duration = 0
	}
	timer.Reset(duration)
}

func safeRun(ctx context.Context, name string, fn Func) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("corn job %s panic: %v", name, r)
		}
	}()

	select {
	case <-ctx.Done():
		return
	default:
		fn(ctx)
	}
}

// Parse 解析 corn 表达式。
// 支持：
//   - 5 字段：minute hour day-of-month month day-of-week
//   - 6 字段：second minute hour day-of-month month day-of-week
//   - @every 1m / @every 30s
//
// 字段支持 *、*/n、a-b、a-b/n、逗号列表和具体数字。
func Parse(spec string) (schedule, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty spec")
	}
	if strings.HasPrefix(spec, "@every ") {
		duration, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(spec, "@every ")))
		if err != nil {
			return nil, err
		}
		if duration <= 0 {
			return nil, fmt.Errorf("@every duration must be positive")
		}
		return everySchedule{interval: duration}, nil
	}

	fields := strings.Fields(spec)
	if len(fields) == 5 {
		fields = append([]string{"0"}, fields...)
	}
	if len(fields) != 6 {
		return nil, fmt.Errorf("expected 5 or 6 fields, got %d", len(fields))
	}

	seconds, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("second: %w", err)
	}
	minutes, err := parseField(fields[1], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	hours, err := parseField(fields[2], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	dom, err := parseField(fields[3], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	months, err := parseField(fields[4], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	dow, err := parseField(fields[5], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	if _, ok := dow[7]; ok {
		dow[0] = struct{}{}
		delete(dow, 7)
	}

	return cronSchedule{seconds: seconds, minutes: minutes, hours: hours, dom: dom, months: months, dow: dow}, nil
}

func parseField(field string, min int, max int) (map[int]struct{}, error) {
	values := make(map[int]struct{})
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty part")
		}

		base := part
		step := 1
		if strings.Contains(part, "/") {
			parts := strings.Split(part, "/")
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid step %q", part)
			}
			base = parts[0]
			parsedStep, err := strconv.Atoi(parts[1])
			if err != nil || parsedStep <= 0 {
				return nil, fmt.Errorf("invalid step %q", parts[1])
			}
			step = parsedStep
		}

		start, end, err := parseRange(base, min, max)
		if err != nil {
			return nil, err
		}
		for i := start; i <= end; i += step {
			values[i] = struct{}{}
		}
	}
	return values, nil
}

func parseRange(base string, min int, max int) (int, int, error) {
	if base == "*" || base == "" {
		return min, max, nil
	}
	if strings.Contains(base, "-") {
		parts := strings.Split(base, "-")
		if len(parts) != 2 {
			return 0, 0, fmt.Errorf("invalid range %q", base)
		}
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range start %q", parts[0])
		}
		end, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range end %q", parts[1])
		}
		if start > end || start < min || end > max {
			return 0, 0, fmt.Errorf("range %q out of bounds %d-%d", base, min, max)
		}
		return start, end, nil
	}

	value, err := strconv.Atoi(base)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid value %q", base)
	}
	if value < min || value > max {
		return 0, 0, fmt.Errorf("value %d out of bounds %d-%d", value, min, max)
	}
	return value, value, nil
}
