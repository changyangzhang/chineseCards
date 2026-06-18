package model

import "time"

type Kind string

const (
	KindWord                 Kind = "word"
	KindPhrase               Kind = "phrase"
	KindVerb                 Kind = "verb"
	KindSentence             Kind = "sentence"
	KindSentenceUntranslated Kind = "sentence_untranslated"
	KindExampleSentence      Kind = "example_sentence"
)

type CardType string

const (
	CardTypeFlashZhEn CardType = "flash_zh_en"
	CardTypeFlashEnZh CardType = "flash_en_zh"
	CardTypeCloze     CardType = "cloze"
)

type ParsedEntry struct {
	Kind       Kind
	Chinese    string // canonical hanzi (trimmed, lowercased for any latin letters)
	ChineseRaw string // hanzi as typed
	Pinyin     string // toned pinyin (e.g. "nǐ hǎo"); empty until enriched
	English    string // "" if untranslated
}

type Entry struct {
	ID                 int64
	NoteID             int64
	SourceEntryID      *int64
	Kind               Kind
	Chinese            string
	ChineseRaw         string
	Pinyin             string
	English            *string
	SuggestedClozeWord *string
	GrammarNote        *string
	TypoCorrection     *string
	Hash               string
	EnrichedAt         *time.Time
	CreatedAt          time.Time
}

type Card struct {
	ID           int64
	EntryID      int64
	CardType     CardType
	Front        string
	Back         string
	ClozeAnswer  *string
	Hash         string
	EaseFactor   float64
	IntervalDays int
	Repetitions  int
	DueAt        time.Time
	LastReviewed *time.Time
	Lapses       int
	CreatedAt    time.Time
}
