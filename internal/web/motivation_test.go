package web

import (
	"testing"
	"time"
)

// decideReviewNudge is pure — these tests cover every triggering branch.

func TestDecideReviewNudge_FirstCardOfDayWelcome(t *testing.T) {
	pool, tone, ok := decideReviewNudge(reviewNudgeInput{
		JustGradedCorrect: true,
		ReviewsTodayAfter: 1,
		Streak:            1,
		RandomRoll:        0.99, // far above the random gate; first-card bypasses
	})
	if !ok || pool != "welcome" || tone != toneWelcome {
		t.Errorf("got (%q, %q, %v); want welcome on first card of day", pool, tone, ok)
	}
}

func TestDecideReviewNudge_FirstCardOfStreakMilestone(t *testing.T) {
	cases := []struct {
		streak int
		want   string
	}{
		{3, "streak3"},
		{7, "streak7"},
		{14, "streak14"},
		{30, "streak30"},
		{60, "streak60"},
		{100, "streak100"},
	}
	for _, c := range cases {
		pool, tone, ok := decideReviewNudge(reviewNudgeInput{
			JustGradedCorrect: true,
			ReviewsTodayAfter: 1,
			Streak:            c.streak,
			RandomRoll:        0.99,
		})
		if !ok || pool != c.want || tone != toneStreak {
			t.Errorf("streak=%d → (%q, %q, %v); want %q", c.streak, pool, tone, ok, c.want)
		}
	}
}

func TestDecideReviewNudge_EveryTenth(t *testing.T) {
	pool, tone, ok := decideReviewNudge(reviewNudgeInput{
		JustGradedCorrect: true,
		ReviewsTodayAfter: 10,
		RandomRoll:        0.99,
	})
	if !ok || pool != "milestone10" || tone != toneMilestone {
		t.Errorf("got (%q, %q, %v); want milestone10", pool, tone, ok)
	}
}

func TestDecideReviewNudge_LapseAfterStreak(t *testing.T) {
	// Index 0 is the just-applied wrong; indices 1..3 are corrects.
	pool, tone, ok := decideReviewNudge(reviewNudgeInput{
		JustGradedCorrect: false,
		ReviewsTodayAfter: 5,
		TrailingRatings:   []int{1, 4, 4, 5, 4}, // wrong then 4 correct
		RandomRoll:        0.99,
	})
	if !ok || pool != "comfort" || tone != toneComfort {
		t.Errorf("got (%q, %q, %v); want comfort after lapse", pool, tone, ok)
	}
}

func TestDecideReviewNudge_LapseWithoutStreakStaysQuiet(t *testing.T) {
	// Wrong answer but only 1 trailing correct — not a lapse moment.
	_, _, ok := decideReviewNudge(reviewNudgeInput{
		JustGradedCorrect: false,
		ReviewsTodayAfter: 3,
		TrailingRatings:   []int{1, 4, 1, 4},
		RandomRoll:        0.99,
	})
	if ok {
		t.Errorf("should stay quiet on a wrong with no prior streak")
	}
}

func TestDecideReviewNudge_RandomSprinkleFires(t *testing.T) {
	pool, tone, ok := decideReviewNudge(reviewNudgeInput{
		JustGradedCorrect: true,
		ReviewsTodayAfter: 7,
		RandomRoll:        0.05, // under 0.10 gate
	})
	if !ok || pool != "warm" || tone != toneWarm {
		t.Errorf("got (%q, %q, %v); want warm sprinkle", pool, tone, ok)
	}
}

func TestDecideReviewNudge_RandomSprinkleNonFire(t *testing.T) {
	_, _, ok := decideReviewNudge(reviewNudgeInput{
		JustGradedCorrect: true,
		ReviewsTodayAfter: 7,
		RandomRoll:        0.5, // above 0.10 gate
	})
	if ok {
		t.Errorf("random sprinkle should not fire at roll=0.5")
	}
}

func TestDecideReviewNudge_RandomSprinkleOnlyOnCorrect(t *testing.T) {
	// Wrong answer + no lapse-streak condition should NOT sprinkle.
	_, _, ok := decideReviewNudge(reviewNudgeInput{
		JustGradedCorrect: false,
		ReviewsTodayAfter: 7,
		TrailingRatings:   []int{1, 4, 1}, // wrong, one right, wrong — not enough trail
		RandomRoll:        0.01,
	})
	if ok {
		t.Errorf("random sprinkle must not fire on wrong answers")
	}
}

func TestTrailingCorrects(t *testing.T) {
	if got := trailingCorrects([]int{5, 4, 4, 1, 4}, 0); got != 3 {
		t.Errorf("from=0 → %d, want 3", got)
	}
	if got := trailingCorrects([]int{1, 4, 4, 4}, 1); got != 3 {
		t.Errorf("from=1 → %d, want 3", got)
	}
	if got := trailingCorrects([]int{1, 1, 1}, 1); got != 0 {
		t.Errorf("from=1 → %d, want 0", got)
	}
	if got := trailingCorrects(nil, 0); got != 0 {
		t.Errorf("empty → %d, want 0", got)
	}
}

func TestMotivator_Debounce(t *testing.T) {
	m := &motivator{lastByPool: map[string]int{}}
	if _, ok := m.serveDebounced("warm", toneWarm, 30*time.Second); !ok {
		t.Fatalf("first serve must succeed")
	}
	// Second serve immediately after must be debounced.
	if _, ok := m.serveDebounced("warm", toneWarm, 30*time.Second); ok {
		t.Errorf("second serve within debounce window must skip")
	}
	// Manually rewind so the next call succeeds, simulating elapsed time.
	m.lastShown = time.Now().Add(-31 * time.Second)
	if _, ok := m.serveDebounced("warm", toneWarm, 30*time.Second); !ok {
		t.Errorf("serve after debounce window must succeed")
	}
}

func TestMotivator_ServeBypassesDebounce(t *testing.T) {
	m := &motivator{lastByPool: map[string]int{}}
	if _, ok := m.serveDebounced("warm", toneWarm, 30*time.Second); !ok {
		t.Fatalf("setup serve must succeed")
	}
	// serve (unconditional) must still fire even though we're inside the debounce window.
	if _, ok := m.serve("milestone10", toneMilestone); !ok {
		t.Errorf("unconditional serve must fire regardless of debounce")
	}
}

func TestMotivator_AvoidsImmediateRepeat(t *testing.T) {
	// Add an artificial small pool to drive the no-repeat path deterministically.
	// We use the "comfort" pool which has multiple lines.
	m := &motivator{lastByPool: map[string]int{}}
	first, ok1 := m.serve("comfort", toneComfort)
	if !ok1 {
		t.Fatalf("first serve must succeed")
	}
	// 100 serves: every result must be a valid line, and the picker should
	// never serve the same index twice in immediate succession.
	prev := first.Text
	for i := 0; i < 100; i++ {
		got, ok := m.serve("comfort", toneComfort)
		if !ok {
			t.Fatalf("iteration %d: serve failed", i)
		}
		if got.Text == prev && len(nudgePool["comfort"]) > 1 {
			t.Errorf("iteration %d: served same line twice in a row: %q", i, got.Text)
		}
		prev = got.Text
	}
}

func TestPickEmptyStateNudge(t *testing.T) {
	// Cold state: no reviews, nothing reached. Stay quiet.
	if n := pickEmptyStateNudge(&emptyState{DailyTarget: 10, ReviewsToday: 0}); n != nil {
		t.Errorf("cold state nudged: %+v", n)
	}
	// Daily limit reached.
	if n := pickEmptyStateNudge(&emptyState{DailyTarget: 10, ReviewsToday: 10}); n == nil {
		t.Errorf("limit reached should nudge")
	}
	// Caught up (reviewed some, next due in the future).
	if n := pickEmptyStateNudge(&emptyState{DailyTarget: 10, ReviewsToday: 3, NextDueRel: "in 5h"}); n == nil {
		t.Errorf("caught-up state should nudge")
	}
	// Nil safety.
	if n := pickEmptyStateNudge(nil); n != nil {
		t.Errorf("nil empty state should not nudge")
	}
}
