package web

import "testing"

func TestRetryQueue_FIFO(t *testing.T) {
	var q retryQueue
	q.push(1)
	q.push(2)
	q.push(3)
	if got := q.len(); got != 3 {
		t.Fatalf("len = %d, want 3", got)
	}
	for _, want := range []int64{1, 2, 3} {
		got, ok := q.pop()
		if !ok || got != want {
			t.Errorf("pop = (%d, %v), want (%d, true)", got, ok, want)
		}
	}
	if _, ok := q.pop(); ok {
		t.Errorf("pop on empty queue should return ok=false")
	}
}

func TestRetryQueue_PushIsIdempotent(t *testing.T) {
	var q retryQueue
	q.push(7)
	q.push(7)
	q.push(7)
	if got := q.len(); got != 1 {
		t.Errorf("duplicate pushes should be no-ops; len = %d, want 1", got)
	}
}

func TestRetryQueue_RemoveAndContains(t *testing.T) {
	var q retryQueue
	q.push(1)
	q.push(2)
	q.push(3)
	if !q.contains(2) {
		t.Errorf("expected contains(2) true")
	}
	q.remove(2)
	if q.contains(2) {
		t.Errorf("after remove, contains(2) should be false")
	}
	if got := q.len(); got != 2 {
		t.Errorf("len after remove = %d, want 2", got)
	}
	// Popping order preserved for the survivors.
	if got, _ := q.pop(); got != 1 {
		t.Errorf("first pop = %d, want 1", got)
	}
	if got, _ := q.pop(); got != 3 {
		t.Errorf("second pop = %d, want 3", got)
	}
}

func TestRetryQueue_MoveToEndPattern(t *testing.T) {
	// The pattern the review handler uses: pop the head, serve it, then on
	// wrong answer push it back. It should land at the tail.
	var q retryQueue
	q.push(1)
	q.push(2)
	q.push(3)

	head, _ := q.pop() // serve card 1
	if head != 1 {
		t.Fatalf("head = %d, want 1", head)
	}
	q.push(1) // wrong on card 1, re-enqueue

	// Queue should now be [2, 3, 1].
	if got, _ := q.pop(); got != 2 {
		t.Errorf("after re-enqueue, first pop = %d, want 2", got)
	}
	if got, _ := q.pop(); got != 3 {
		t.Errorf("second pop = %d, want 3", got)
	}
	if got, _ := q.pop(); got != 1 {
		t.Errorf("third pop = %d, want 1 (moved to end)", got)
	}
}
