package spaces

import "sync"

// queue holds work in the order it came, never making whoever adds to it wait.
type queue struct {
	mu    sync.Mutex
	items []func()
	ready chan struct{}
}

func newQueue() *queue { return &queue{ready: make(chan struct{}, 1)} }

func (q *queue) push(f func()) {
	q.mu.Lock()
	q.items = append(q.items, f)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// drain runs everything queued, in order, including what is added meanwhile.
func (q *queue) drain() {
	for {
		q.mu.Lock()
		if len(q.items) == 0 {
			q.mu.Unlock()
			return
		}
		f := q.items[0]
		q.items = q.items[1:]
		q.mu.Unlock()
		f()
	}
}
