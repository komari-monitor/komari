package writequeue

import "sync"

// Queue runs submitted tasks in submission order without blocking the caller.
// A task may block on the underlying stream; only the queue worker waits for it.
type Queue struct {
	mu      sync.Mutex
	tasks   []func()
	running bool
}

func (q *Queue) Submit(task func()) {
	if task == nil {
		return
	}
	q.mu.Lock()
	q.tasks = append(q.tasks, task)
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.mu.Unlock()
	go q.run()
}

func (q *Queue) run() {
	for {
		q.mu.Lock()
		if len(q.tasks) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		task := q.tasks[0]
		q.tasks[0] = nil
		q.tasks = q.tasks[1:]
		q.mu.Unlock()
		task()
	}
}
