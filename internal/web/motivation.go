package web

import (
	"context"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"chineseCards/internal/store"
)

// Nudge is a sweet, warm motivational line surfaced occasionally in the UI.
// Tone is exposed for CSS hooks (e.g. .nudge-streak vs .nudge-comfort) but
// templates currently treat them uniformly.
type Nudge struct {
	Text string
	Tone string
}

// Tone constants — kept short so they're cheap CSS class suffixes.
const (
	toneWelcome   = "welcome"
	toneMilestone = "milestone"
	toneStreak    = "streak"
	toneComfort   = "comfort"
	toneWarm      = "warm"
	toneRest      = "rest"
	toneHome      = "home"
)

// nudgePool holds copy. Keep lines short, warm, gently encouraging — no
// shaming, no false urgency, no exclamation pile-ups.
var nudgePool = map[string][]string{
	"welcome": {
		"Hey :) good to see you back.",
		"You came back today — that's already winning.",
		"Welcome back. One card at a time, just like that.",
		"Hi again. Glad you're here.",
		"There you are. Let's do a little Chinese together.",
	},
	"milestone10": {
		"Ten more done. Look at you go.",
		"Quietly stacking the wins. Ten today.",
		"That's ten. You're really doing this.",
		"Ten in the books. Proud of you.",
	},
	"streak3": {
		"Three days in a row. Something's clicking.",
		"Day three! A habit is forming, gently.",
		"Three days running — you showed up.",
	},
	"streak7": {
		"Seven days in a row. Proud of you.",
		"A whole week! That counts for a lot.",
		"Seven days. You're a person who studies Chinese now.",
	},
	"streak14": {
		"Two weeks straight. You're not playing around.",
		"Fourteen days in. That's real consistency.",
	},
	"streak30": {
		"Thirty days. Thirty days! You're a person who studies Chinese now.",
		"A whole month of showing up. That's beautiful.",
	},
	"streak60": {
		"Sixty days. You've made this part of who you are.",
		"Two months running. Quiet, steady, real.",
	},
	"streak100": {
		"One hundred days. One hundred! Take a moment with that.",
		"100 days. You're someone who finishes things.",
	},
	"comfort": {
		"Missed one — no big deal. That's how learning sticks.",
		"That one's tricky. You'll get it next time.",
		"Happens to everyone who actually practices. Keep going :)",
		"One miss after so many rights — perfectly normal.",
	},
	"warm": {
		"You can do this. Keep going.",
		"Look at you, keeping at it.",
		"Believe in yourself — you're closer than you think.",
		"Slow and steady. You're getting there.",
		"That was a good one. Onward.",
		"You're allowed to be proud of this.",
	},
	"rest": {
		"All done for today. Rest is part of the practice.",
		"You showed up. That's the whole game. See you tomorrow.",
		"Done for today — well earned. Have a nice evening :)",
		"Today's quota: cleared. Go enjoy something.",
	},
	"homeStreak": {
		"{N} days in a row. You're building something.",
		"{N}-day streak. Quietly impressive.",
		"{N} days running — keep that warm momentum.",
	},
	"homeProgress": {
		"{N} reviews behind you. That's real progress.",
		"You've reviewed {N} cards. Every one of them counts.",
		"{N} done so far. You're still here, and that matters.",
	},
	"homeWarm": {
		"Hey :) ready when you are.",
		"Welcome back. Take your time.",
		"Small steps, every day. You've got this.",
	},
}

// motivator holds picker state: a process-local debounce timestamp and a
// per-pool "last index served" memory so the same line never appears twice
// in a row.
type motivator struct {
	mu         sync.Mutex
	lastShown  time.Time
	lastByPool map[string]int
}

var nudger = &motivator{lastByPool: map[string]int{}}

// serve unconditionally picks a line from `pool`. Used for milestone-grade
// triggers that should bypass the random/debounce gate.
func (m *motivator) serve(pool, tone string) (*Nudge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pickLocked(pool, tone)
}

// serveDebounced is like serve but skips when the last nudge fired more
// recently than minGap. Used for random sprinkles so back-to-back cards
// never both carry a nudge.
func (m *motivator) serveDebounced(pool, tone string, minGap time.Duration) (*Nudge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.lastShown.IsZero() && time.Since(m.lastShown) < minGap {
		return nil, false
	}
	return m.pickLocked(pool, tone)
}

func (m *motivator) pickLocked(pool, tone string) (*Nudge, bool) {
	lines := nudgePool[pool]
	if len(lines) == 0 {
		return nil, false
	}
	idx := rand.IntN(len(lines))
	if prev, seen := m.lastByPool[pool]; seen && idx == prev && len(lines) > 1 {
		idx = (idx + 1) % len(lines)
	}
	m.lastByPool[pool] = idx
	m.lastShown = time.Now()
	return &Nudge{Text: lines[idx], Tone: tone}, true
}

// reviewNudgeInput is the pure-data input to decideReviewNudge. Keeping the
// decision logic pure makes it trivial to unit-test all the branching.
type reviewNudgeInput struct {
	JustGradedCorrect bool    // current card was answered correctly
	ReviewsTodayAfter int     // count of today's reviews INCLUDING the just-applied one
	Streak            int     // calendar-day streak ending today (only used on first card of day)
	TrailingRatings   []int   // N most recent ratings, time desc, INCLUDING the just-applied
	RandomRoll        float64 // injected for testability (0.0..1.0)
}

// decideReviewNudge picks which pool to draw from, given the state of the
// session. Returns "" if no nudge should fire. Pure function — no DB, no
// rng beyond the injected RandomRoll.
func decideReviewNudge(in reviewNudgeInput) (pool, tone string, ok bool) {
	// First card of the day — and possibly a streak milestone moment.
	if in.ReviewsTodayAfter == 1 {
		switch in.Streak {
		case 3:
			return "streak3", toneStreak, true
		case 7:
			return "streak7", toneStreak, true
		case 14:
			return "streak14", toneStreak, true
		case 30:
			return "streak30", toneStreak, true
		case 60:
			return "streak60", toneStreak, true
		case 100:
			return "streak100", toneStreak, true
		}
		return "welcome", toneWelcome, true
	}

	// Every-10th milestone.
	if in.ReviewsTodayAfter > 0 && in.ReviewsTodayAfter%10 == 0 {
		return "milestone10", toneMilestone, true
	}

	// Lapse after a run of correct answers — comfort, not silence.
	if !in.JustGradedCorrect && trailingCorrects(in.TrailingRatings, 1) >= 3 {
		return "comfort", toneComfort, true
	}

	// Random sprinkle, correct answers only. 10% gate.
	if in.JustGradedCorrect && in.RandomRoll < 0.10 {
		return "warm", toneWarm, true
	}

	return "", "", false
}

// trailingCorrects counts how many consecutive ratings >= 4 (Good/Easy) the
// slice contains starting at index `from`. Ratings are in time-desc order.
func trailingCorrects(ratings []int, from int) int {
	n := 0
	for i := from; i < len(ratings); i++ {
		if ratings[i] >= 4 {
			n++
			continue
		}
		break
	}
	return n
}

// pickReviewNudge is the side-effecting wrapper used by handleReviewPost.
// It queries the store for context, then asks decideReviewNudge what to do,
// then asks the motivator to serve a line (with debounce on the random
// sprinkle, no debounce on real milestones).
//
// Errors are swallowed deliberately — a nudge is a nice-to-have, never a
// reason to fail a request.
func pickReviewNudge(ctx context.Context, st *store.Store, justGradedCorrect bool) *Nudge {
	count, err := st.CountReviewsToday(ctx)
	if err != nil {
		return nil
	}
	ratings, _ := st.LastNRatings(ctx, 10)

	streak := 0
	if count == 1 {
		// Only compute streak when it's the first card of the day —
		// streak-milestone messages only fire then. Saves a query on every
		// other review.
		dates, _ := st.ReviewDatesDesc(ctx, 200)
		streak, _ = computeStreak(dates)
	}

	pool, tone, ok := decideReviewNudge(reviewNudgeInput{
		JustGradedCorrect: justGradedCorrect,
		ReviewsTodayAfter: count,
		Streak:            streak,
		TrailingRatings:   ratings,
		RandomRoll:        rand.Float64(),
	})
	if !ok {
		return nil
	}

	// Real milestones bypass the debounce; random sprinkle honors it.
	if pool == "warm" {
		n, _ := nudger.serveDebounced(pool, tone, 30*time.Second)
		return n
	}
	n, _ := nudger.serve(pool, tone)
	return n
}

// pickEmptyStateNudge returns a warm sign-off for the daily-done empty state.
// Fires when the user actually got somewhere today:
//   - Daily limit reached (ReviewsToday >= DailyTarget).
//   - All caught up (no card due now AND at least one review today).
//
// Stays quiet on cold empty states (no cards in deck, nothing reviewed yet).
func pickEmptyStateNudge(es *emptyState) *Nudge {
	if es == nil {
		return nil
	}
	limitReached := es.DailyTarget > 0 && es.ReviewsToday >= es.DailyTarget
	caughtUp := es.ReviewsToday > 0 && es.NextDueRel != ""
	if limitReached || caughtUp {
		n, _ := nudger.serve("rest", toneRest)
		return n
	}
	return nil
}

// pickHomeNudge returns a quiet line for the home page. Chooses between
// streak-flavoured, progress-flavoured, and generic-warm pools based on
// what the user has accomplished.
func pickHomeNudge(ctx context.Context, st *store.Store, totalReviews int) *Nudge {
	dates, _ := st.ReviewDatesDesc(ctx, 200)
	streak, _ := computeStreak(dates)

	switch {
	case streak >= 3:
		return renderHomeNudge("homeStreak", toneHome, streak)
	case totalReviews >= 10:
		return renderHomeNudge("homeProgress", toneHome, totalReviews)
	case totalReviews > 0:
		return renderHomeNudge("homeWarm", toneHome, 0)
	}
	return nil
}

// renderHomeNudge picks a line from `pool` and substitutes {N} with `n`.
func renderHomeNudge(pool, tone string, n int) *Nudge {
	out, ok := nudger.serve(pool, tone)
	if !ok {
		return nil
	}
	if strings.Contains(out.Text, "{N}") {
		out.Text = strings.ReplaceAll(out.Text, "{N}", strconv.Itoa(n))
	}
	return out
}
