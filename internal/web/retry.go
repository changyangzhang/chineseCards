package web

import "sync"

// retryQueue holds card IDs the learner missed during the current server-
// lifetime session. Wrong picks push a card here (instead of triggering a
// full SM-2 "Again" review); correct picks remove it. When the normal SRS
// queue is exhausted — daily target reached or nothing else due — the
// review handler drains this queue in FIFO order, so missed cards keep
// coming back until the learner gets them right, and none of the retry
// attempts count toward the daily target.
//
// In-memory, process-lifetime, single-user. Personal-app trade-off:
// server restart / auto-stop clears the queue, but that's harmless — the
// missed cards weren't SRS-recorded, so they re-appear naturally through
// the normal queue on the next session.
type retryQueue struct {
	mu  sync.Mutex
	ids []int64
}

// push appends `id` to the tail. Idempotent — a duplicate push is a no-op
// so the queue never grows unbounded from repeated wrongs on the same
// card.
func (q *retryQueue) push(id int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, x := range q.ids {
		if x == id {
			return
		}
	}
	q.ids = append(q.ids, id)
}

// pop removes and returns the head. Returns (0, false) when empty.
func (q *retryQueue) pop() (int64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.ids) == 0 {
		return 0, false
	}
	id := q.ids[0]
	q.ids = q.ids[1:]
	return id, true
}

// remove drops any occurrence of `id`. Called on correct answers and on
// mid-session card deletion so a since-vanished card doesn't get popped
// later.
func (q *retryQueue) remove(id int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.ids[:0]
	for _, x := range q.ids {
		if x != id {
			out = append(out, x)
		}
	}
	q.ids = out
}

// contains reports whether `id` is currently pending in the queue.
func (q *retryQueue) contains(id int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, x := range q.ids {
		if x == id {
			return true
		}
	}
	return false
}

// len returns the number of pending retries.
func (q *retryQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.ids)
}
