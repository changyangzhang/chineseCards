# Chinese Cards

A personal, Clozemaster-style Mandarin Chinese learning web app for **absolute beginners** (A1 / HSK 1). Paste raw lesson notes — hanzi, pinyin, English, or any mix — get AI-enriched flashcards back, and review them daily with multiple-choice quizzes that vary every time you see a card. There's also a built-in tutor chat, a six-month contribution heatmap, an in-session retry loop for wrong answers, and a Word of the Day.

Single-user app. Built for one person's daily habit; not designed for shared decks or multi-tenancy. Forked from a Swedish-learning sibling and re-tuned for simplified characters (简体字), toned pinyin, and Mandarin TTS.

**Live**: [cy-chinese-cards.fly.dev](https://cy-chinese-cards.fly.dev)

---

## Contents

1. [What's on each page](#whats-on-each-page)
2. [The review flow](#the-review-flow)
3. [Habit & motivation](#habit--motivation)
4. [Import & AI enrichment](#import--ai-enrichment)
5. [Tutor chat](#tutor-chat)
6. [Auth & sessions](#auth--sessions)
7. [Stack](#stack)
8. [Quick start (local)](#quick-start-local)
9. [Environment variables](#environment-variables)
10. [Deploy to Fly.io](#deploy-to-flyio)
11. [Project layout](#project-layout)
12. [Roadmap](#roadmap)
13. [Privacy](#privacy)

---

## What's on each page

| Route | Purpose |
|---|---|
| `/` | Today's due/new/total counts, six-month contribution grid, trophy shelf, motivational nudge, **Word of the Day** |
| `/review` | Multiple-choice quiz with progress bar + auto-TTS + retry loop |
| `/chat` | Message thread with the Mandarin tutor (OpenAI) |
| `/import` | Paste text / upload image for AI parse + enrichment |
| `/cards` | Browse the deck, edit / delete cards, accept AI typo fixes |
| `/stats` | Streak, accuracy, six-month contribution grid, upcoming due chart |
| `/settings` | Daily review target |
| `/login` · `/logout` | Form-based auth (single account, env-configured) |

---

## The review flow

### Multiple-choice modes

Every review is multiple-choice. Each render of a card picks **a fresh presentation at random**:

| Mode | Prompt | Choices |
|---|---|---|
| `mc_translate` | Chinese hanzi (+ pinyin underneath) | 4 English options |
| `mc_translate_rev` | English | 4 Chinese options (each with pinyin) |
| `mc_cloze` | Chinese sentence with one word blanked (+ pinyin of the surrounding context) | 4 Chinese word options (each with pinyin) |

Reviewing the same card twice in a row never looks identical:
- Distractors are pulled fresh each render (`ORDER BY RANDOM()`).
- For cloze cards, the blanked word uses the LLM's suggested target when available and falls back to whitespace tokenisation.
- For word entries with an OpenAI-generated example sentence attached, cloze mode uses that example so you drill the word in context.

Auto-graded: correct → SM-2 `Good`. Keyboard shortcuts 1–4 pick MC options.

### Pinyin everywhere

Every hanzi in the quiz — front, choices, cloze context — carries toned pinyin underneath. DB-stored pinyin (from LLM enrichment) is preferred; the offline [`go-pinyin`](https://github.com/mozillazg/go-pinyin) library fills in for anything the LLM never touched, so cards imported without an OpenAI key still get readings.

Pinyin on the *blanked* cloze sentence is computed from the masked string, so it doesn't leak the answer.

### Auto text-to-speech after every answer

The moment you pick, the result ribbon appears and the Chinese side of the just-finished card is spoken automatically via the browser's Web Speech API (`lang="zh-CN"`):
- **Cloze**: the fully-filled sentence.
- **Reverse translate**: the correct Chinese answer.
- **Forward translate**: the Chinese front.

Manual 🔊 buttons remain next to the ribbon, front hanzi, and every Chinese choice for replay.

### Session retry loop (wrong picks don't count)

If you pick the wrong option, the card is quietly appended to the end of the current session and comes back until you get it right. Retries **don't insert a review row, don't advance the SM-2 schedule, and don't count against the daily target** — only the eventual correct answer does. The card's `lapses` counter increments so "I struggled here" is still visible in `/stats`. Retried cards get a small **🔁 second try** badge next to the mode indicator.

### Progress bar

At the top of every card: a `N / target today` counter and a thin gradient bar that fills as you rack up correct answers. Refreshes on every HTMX swap.

### Daily cap and empty-state options

When the daily budget is spent, the empty state explains *why* and offers:
- **Practice 5 more / 10 more** — bump the daily target permanently (persisted to SQLite).
- **Review next card anyway** — bypass the SM-2 schedule and serve the soonest-due card (early review still updates state).
- **🗑 Delete this card** — from the review screen, drop a card mid-session if it's not worth learning.

### Spaced repetition (SM-2)

Standard Anki-style SM-2:
- Default ease factor 2.5; floored at 1.3.
- Intervals: 1 day → 6 days → `prev_interval × ease_factor`.
- **Daily review target** (configurable; default 10) covers BOTH new and due-again cards combined. The queue serves ~30% new / 70% due-again so you keep learning while you maintain.

---

## Habit & motivation

The whole app is designed around one habit: *show up daily*. Several small features nudge you toward that:

### Motivational nudges

Sweet, warm, cheerful-and-light — sentence case, gentle exclamations, occasional 🙂 warmth. Rare (~1 in 10 cards + milestones). Surfaces:
- **Post-answer ribbon** — occasional line next to the ✓ / ✗ indicator.
- **Empty state** — sweet sign-off when the daily target is reached (`"You showed up. That's the whole game. See you tomorrow."`).
- **Home page** — quiet warmth under the stat grid on any day with ≥1 review.

### Personalised first-card greeting

The first card of each day opens with a bespoke line combining time-of-day + your name + streak length: `"Morning, Joakim. Day 8 in a row 🌱"` / `"Evening, Joakim. Fresh start today."` — degrades gracefully if any piece is missing.

### Streak milestones

At day 3 / 7 / 14 / 30 / 60 / 100 in a row, a bigger celebratory line fires on the first card of that day (`"Thirty days. Thirty days! You're a person who studies Chinese now."`).

### Trophy shelf (lifetime reviews)

Persistent 🏅 30 / 🏆 100 / 💎 500 / 🌟 1000 / 🐉 5000 chips on the home page. Crossed thresholds are filled; the next unreached one appears dashed as a "so close" preview.

### Personal-best-today callout

Fires **at most once per day**, on a correct pick, when today's accuracy strictly beats every prior day in the trailing 30 days (with minimum-sample guardrails so a lucky answer at review #2 doesn't crown a PB).

### Six-month contribution grid

GitHub-style 7×26 heatmap on the landing page — one square per day, shaded by review count (five levels). Sits above the trophy shelf so it's the second thing you see (after today's due/new counts). Six months at a glance on the landing page is more motivating than any single "streak: 12" number.

### Comfort on lapses

If you get one wrong after a run of ≥3 correct, you get a warm line instead of shame: `"Missed one, no big deal. That's how learning sticks."`

### Word of the Day

Home page shows one random word from your deck each day with a **fresh** LLM-generated A1 example sentence — hanzi, pinyin, English — regenerated at midnight. Cached in a `word_of_day` SQLite table so refreshing doesn't burn OpenAI quota. Even if you don't review, opening the app to see today's word is a habit hook.

---

## Import & AI enrichment

Two input modes on `/import`:

1. **Paste text** into the textarea. Works for the classic `Chinese = English` format and the heuristic parser handles it offline (no API key needed).
2. **Upload a file** — image (PNG/JPG/HEIC/WebP), PDF, or .txt/.md. Up to 8 MB. Photos of handwritten or printed Chinese notes work directly; OpenAI's vision-capable model does OCR + parsing in a single call.

Each entry produces **exactly one card** (no duplicates per concept). Hash-based dedup: re-pasting the same notes is a no-op.

When `OPENAI_API_KEY` is set, OpenAI GPT-5-mini handles BOTH parsing and enrichment in one call, tuned for **absolute beginners (A1 / HSK 1)**:
- Foundational greetings (`你好`, `谢谢`, `再见`), pronouns, copulas, basic verbs, numbers, question particles — none skipped as "too elementary".
- Simplified characters (简体字) with toned pinyin.
- One short A1-level example sentence per word entry (used later for cloze prompts).
- Smart cloze target words (never a particle / pronoun / copula).
- One-sentence grammar notes.
- Typo flags surfaced on `/cards` for one-click Accept / Dismiss.

Retries on transient 503/429 with backoff. The app degrades gracefully when `OPENAI_API_KEY` is unset: the textarea path still works via the heuristic parser (pinyin is filled by the offline library, no examples generated).

---

## Tutor chat

The **Chat** tab (`/chat`) opens a persistent conversation with a OpenAI-backed Mandarin tutor tuned for absolute beginners. Every hanzi reply comes with pinyin and English; tone stays warm and concise. Multi-turn context is preserved across messages *and* across page reloads — the whole thread lives in a `chat_messages` SQLite table.

- **Enter** sends, **Shift+Enter** newline.
- Retries on 429/5xx.
- 🗑 clear button to wipe the thread and start over.
- Fallback message when `OPENAI_API_KEY` is unset.

---

## Auth & sessions

Form-based login at `/login` with an HMAC-signed session cookie (7-day TTL). Credentials come from `BASIC_USER` / `BASIC_PASS` env vars — leave both empty for a no-auth local-dev setup.

The middleware accepts either the cookie **or** an HTTP Basic auth header, so scripts and `curl -u user:pass` keep working. Browsers (GET + `Accept: text/html`) get a 303 to `/login?next=…`; everything else gets a plain 401. Logout button lives in the nav header.

Session HMAC key is derived from the password itself, so rotating `BASIC_PASS` invalidates every existing cookie for free.

---

## Stack

- **Backend**: Go 1.26, `chi` router, `html/template`, `slog`.
- **Storage**: SQLite via `modernc.org/sqlite` (pure Go, no CGO). One file at `/data/chinese.db`.
- **Frontend**: server-rendered HTML + [HTMX](https://htmx.org) for partial swaps. Hand-rolled CSS (no Tailwind, no framework). Vanilla JS for keyboard shortcuts, TTS, and the chat send-on-Enter behavior.
- **AI**: `github.com/sashabaranov/go-openai`, `responseSchema` for structured output.
- **Pinyin**: `github.com/mozillazg/go-pinyin` for offline hanzi → toned-pinyin conversion.
- **Deploy**: Docker (distroless base, ~8 MB binary). Ships to Fly.io with the included `fly.toml`.

---

## Quick start (local)

```bash
git clone https://github.com/changyangzhang/chineseCards.git
cd chineseCards

cp .env.example .env
# Edit .env — at minimum, set OPENAI_API_KEY (free at
# https://platform.openai.com/api-keys). BASIC_USER/BASIC_PASS optional
# for local; required for any public deployment.

docker compose up -d --build
open http://127.0.0.1:8080
```

If port 8080 is taken on your machine, set `HTTP_PORT=8765` (or anything free) in `.env` and re-run `docker compose up -d`.

Or without Docker:

```bash
go run . 
# picks up env from your shell / .env if you export it
```

---

## Environment variables

| Var | Default | Purpose |
|---|---|---|
| `OPENAI_API_KEY` | _empty_ | Enables AI enrichment, chat, Word-of-the-Day example generation, and typo detection. Get one at https://platform.openai.com/api-keys. App works without it (pinyin still fills via the offline library). |
| `OPENAI_MODEL` | `gpt-5-mini` | Override the model. |
| `NEW_PER_DAY` | `10` | **Seed only** — first-run default for the daily review target (new + due combined). After that, `/settings` (DB-persisted) wins. |
| `BASIC_USER`, `BASIC_PASS` | _empty_ | Auth credentials. Both empty = auth disabled (fine for localhost; **set both before exposing the app publicly**). |
| `DB_PATH` | `chinese.db` | SQLite file path. Docker compose mounts a host volume at `/data`. |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `HTTP_PORT` | `8080` | Host port (compose only). |

---

## Deploy to Fly.io

```bash
flyctl auth login
flyctl launch --copy-config --no-deploy
flyctl volumes create data --region arn --size 1
flyctl secrets set OPENAI_API_KEY=... BASIC_USER=... BASIC_PASS=...
flyctl deploy
flyctl open
```

`fly.toml` is checked in: single machine, 256 MB, Stockholm region, auto-stop when idle. Realistic cost for personal use: $0–2/month.

Public deployment requires `BASIC_USER` + `BASIC_PASS` to be set, otherwise anyone with the URL can burn your OpenAI quota.

---

## Project layout

```
.
├── main.go                          # thin entrypoint
├── cmd/server/server.go             # wiring: db, router, signal handling
├── internal/
│   ├── cards/generate.go            # entry → card (1:1)
│   ├── config/config.go             # env-var loading
│   ├── llm/                         # OpenAI client: parse/enrich, chat, WOTD example
│   ├── model/model.go               # Kind, CardType enums + shared types
│   ├── parser/                      # heuristic parser + Chinese stop-words
│   ├── srs/sm2.go                   # SM-2 math + tests
│   ├── store/                       # SQLite + embedded schema + queries
│   └── web/
│       ├── auth.go                  # cookie-OR-basic auth middleware
│       ├── chat.go                  # tutor chat handlers
│       ├── handlers.go              # review, home, import, cards, stats, WOTD
│       ├── login.go                 # /login GET+POST, /logout
│       ├── motivation.go            # nudge picker (welcome, streaks, PB, comfort)
│       ├── pinyin.go                # offline hanzi → pinyin helper
│       ├── quiz.go                  # rotateBlank, buildChoices, cloze target logic
│       ├── render.go                # template loading + auth-aware funcs
│       ├── retry.go                 # in-session retry queue
│       ├── router.go                # chi routes
│       ├── session.go               # HMAC-signed session cookie
│       ├── static/app.css           # all styling
│       └── templates/*.html         # server-rendered pages
├── Dockerfile                       # multi-stage → distroless
├── docker-compose.yml               # local dev stack
└── fly.toml                         # Fly.io app config
```

---

## Roadmap

- **Streak freezes (earned)**: every 7 clean days grants a freeze; a missed day quietly spends one instead of resetting the streak.
- **Chosen review time + browser notifications**: user sets 8am; app pings when it's time.
- **Weekly recap page**: Sunday-evening summary of the week's reviews, longest session, top-3 tricky words.
- **Traditional-character toggle** on entries.
- **Tone-coloured hanzi**: paint characters by tone on review cards.
- **End-of-session chat prompt**: "You missed 谢谢 twice today — want to ask the tutor?"

---

## Privacy

- Lesson notes, cards, review history, and chat messages live in **one local SQLite file** at `/data/chinese.db`.
- OpenAI API is called at import time (one batched call per note), at chat time (per message), and once per day for Word-of-the-Day example generation. No data leaves the machine for review / SM-2 / stats.
- File uploads (images/PDFs) are NOT stored — bytes go to OpenAI in the request body, then the in-memory copy is garbage-collected. `notes.raw_text` records only the filename + sha256 + size as a stable dedup identifier.
- TTS runs entirely in the browser (Web Speech API) — no audio leaves the device.
- Session cookies are HMAC-signed and marked `HttpOnly` + `SameSite=Lax` + `Secure` on HTTPS.
- The `data/` directory is gitignored; never commit it.
- OpenAI's  may use inputs to improve Google's models per their terms; upgrade to paid to opt out, or paste sensitive notes manually.

---

## License

MIT. See `LICENSE`.
