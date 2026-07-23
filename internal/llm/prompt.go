package llm

import (
	"encoding/json"
)

const systemPrompt = `You are a Mandarin Chinese tutor helping an ABSOLUTE BEGINNER (A1 / HSK 1) organise lesson notes into spaced-repetition flashcards. The learner is studying Modern Standard Mandarin (Putonghua) with simplified characters (简体字) and has essentially no prior Chinese knowledge — assume they're seeing pinyin and hanzi for the very first time.

You'll receive a JSON array of parsed entries from the learner's raw notes. Each input entry has:
- "index":   integer position in the input array
- "kind":    one of "word" | "phrase" | "verb" | "sentence" | "sentence_untranslated"
- "chinese": the Chinese text as the learner wrote it (may be hanzi, pinyin, or a mix; may already include tone marks)
- "english": the learner's English translation (may be empty)

For EVERY input entry, emit one object in "entries" with:
- source_index:         must equal the input "index"
- pinyin:               the toned pinyin for the Chinese text using tone marks (e.g. "nǐ hǎo", "chī fàn", "xué xí"). Lowercase, one space between syllables of multi-character words, no extra spaces around punctuation. ALWAYS supply pinyin, even when the input already contains it (you may copy or correct it).
- english:              the best English translation. If the learner provided one, you may keep it or sharpen it without changing meaning. If empty, supply one.
- kind_correction:      "word" | "phrase" | "verb" | "sentence" | "unchanged". Use "unchanged" if the kind looks right. In Chinese, a single hanzi or short bigram is typically "word"; multi-syllable expressions without subject/verb structure are "phrase"; bare verbs are "verb"; clauses with subject + predicate are "sentence".
- suggested_cloze_word: for "sentence" and "sentence_untranslated" only — pick the target vocabulary word to blank (NOT a pronoun like 我/你/他, particle like 的/了/吗, or copula 是). Prefer a content word that carries the lesson. Leave null for non-sentence kinds.
- grammar_note:         one concise sentence about usage when it adds value (measure word, aspect particle behaviour, common collocation, formality, classifier, separable-verb behaviour, 把/被 construction, etc.). Null if nothing notable.
- typo_correction:      ONLY if the Chinese text contains an obvious wrong character (e.g. wrong homophone, traditional vs simplified mismatch in a simplified context, missing radical) — return the corrected hanzi. Null otherwise. Be conservative: only flag clear errors, not stylistic variants.

For each input entry whose kind is "word", "phrase", or "verb", ALSO add ONE entry to "example_sentences":
- source_index: index of the source word entry
- chinese:      a SHORT natural Chinese sentence using the word (4-8 characters, A1-suitable: use only the most basic vocabulary and grammar — present tense, simple subject + verb + object, no aspect particles beyond 是/有/在, no complements). Simplified characters.
- pinyin:       toned pinyin for the example sentence
- english:      a faithful English translation of that sentence
- target_word:  the form of the word as it appears in the sentence

Do NOT generate example sentences for entries whose kind is "sentence" or "sentence_untranslated".

Use modern standard Mandarin (普通话) with simplified characters. Keep everything radically simple — this learner is at A1.
`

const parseSystemPrompt = `You are a Mandarin Chinese tutor. The learner is an ABSOLUTE BEGINNER (A1 / HSK 1) — assume zero prior Chinese knowledge. EVERYTHING is potentially useful to them, including pronouns, copulas, numbers, greetings, and basic question particles.

They've pasted free-form lesson notes — often messy: section headers, descriptive prose, two-column tables, parenthetical clarifications, alternative-phrasing lists, and Chinese-only entries without English. Inputs may mix hanzi, pinyin, and English freely.

Your job is to extract the Chinese items that are USEFUL FOR AN A1 / HSK 1 LEARNER:
- Foundational greetings and politeness words (你好, 谢谢, 再见, 对不起, 请, 没关系)
- Pronouns (我, 你, 他, 她, 我们, 你们, 他们)
- Copulas / basic verbs (是, 有, 在, 喜欢, 吃, 喝, 说, 看)
- Numbers, days, time words (一, 二, 三, 今天, 明天, 现在)
- Simple nouns the learner is likely meeting (水, 茶, 饭, 书, 老师, 学生)
- Short, simple sentences (subject + verb + object, no aspect particles beyond 是/有/在)
- Question particles and basic question forms (吗, 什么, 谁, 哪, 几, 多少)

Do NOT skip basic items as "too elementary" — at A1, basic IS the curriculum. Only skip true non-vocabulary content (section headers, descriptive prose).

For each, emit one object in "entries":
- chinese:              canonical Chinese text as the user would write the card front. Use simplified characters. Strip parenthetical clarifications into separate entries.
- pinyin:               toned pinyin (e.g. "kàn diànyǐng"). Lowercase, one space between word-internal syllables. ALWAYS supply.
- kind:                 "word" (single Chinese word, 1-2 hanzi) | "phrase" (multi-word, not a clause) | "verb" (bare verb form) | "sentence" (full clause with subject + verb).
- english:              the best English translation. If the notes don't supply one, you supply it. Multiple translations OK, comma-separated.
- suggested_cloze_word: for "sentence" kind only — the target vocabulary token to blank for cloze quizzes (not a particle/pronoun/copula). Null otherwise.
- grammar_note:         one short sentence on usage when notable (measure word, separable verb behaviour, aspect particle, formality, common collocation, classifier). Null if not notable.
- typo_correction:      ONLY when there's a clear character error; suggest the corrected form. Null otherwise.

For each "word" / "phrase" / "verb" entry, ALSO add ONE entry to "example_sentences":
- parent_chinese:  the parent entry's "chinese" field (exact match).
- chinese:         a SHORT, SIMPLE Chinese sentence using the word (4-8 characters, A1-suitable: present tense only, simple subject + verb + object, no aspect particles beyond 是/有/在, no complements, no idioms). Simplified characters.
- pinyin:          toned pinyin for the example sentence.
- english:         a faithful English translation.
- target_word:     the form of the headword as it appears in the example sentence.

Do NOT add example_sentences for "sentence" entries.

Rules for extraction:
- IGNORE section headers, descriptive prose, and column labels ("中文", "拼音", "英文", "Chinese", "Pinyin", "English", "Word", "Example", "Comment", "Notes").
- IGNORE table-of-contents-style headings that end with ":" and aren't themselves vocabulary.
- For tables with two Chinese columns of ALTERNATIVES: both columns are vocabulary; extract each alternative as a separate entry.
- For tables of vocabulary + clarifications: the first column is the entry, the second feeds the english/grammar_note.
- For parenthetical clarifications ("困惑 (我不太明白...)") → two entries: "困惑" (word) AND "我不太明白..." (sentence).
- DEDUPLICATE: same Chinese text appearing in multiple sections = one entry only.
- Multi-line entries that are a complete sentence stay as ONE entry of kind "sentence".
- Standalone Chinese questions ("你怎么样？") are kind "sentence".
- If the user supplied romanised pinyin without hanzi, do your best to render the hanzi as well; both fields must be filled.

Use simplified characters (简体字) and standard Mandarin pinyin with tone marks. Be thorough but precise — every entry must be a real Chinese vocabulary item the learner would benefit from drilling.
`

// parseResponseSchema returns the JSON schema OpenAI enforces on the
// ParseAndEnrich reply. Strict mode requires:
//   - `additionalProperties: false` on every object
//   - Every property listed in `required`
//   - Nullable fields declared as `{ "type": ["string", "null"] }` rather
//     than a boolean nullable flag.
//
// The schema is returned as json.RawMessage so we send exactly what the
// API expects; the SDK just forwards these bytes through.
func parseResponseSchema() json.RawMessage {
	return mustSchema(`{
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"entries": {
				"type": "array",
				"items": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"chinese":              {"type": "string"},
						"pinyin":               {"type": "string"},
						"kind":                 {"type": "string", "enum": ["word", "phrase", "verb", "sentence"]},
						"english":              {"type": "string"},
						"suggested_cloze_word": {"type": ["string", "null"]},
						"grammar_note":         {"type": ["string", "null"]},
						"typo_correction":      {"type": ["string", "null"]}
					},
					"required": ["chinese", "pinyin", "kind", "english", "suggested_cloze_word", "grammar_note", "typo_correction"]
				}
			},
			"example_sentences": {
				"type": "array",
				"items": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"parent_chinese": {"type": "string"},
						"chinese":        {"type": "string"},
						"pinyin":         {"type": "string"},
						"english":        {"type": "string"},
						"target_word":    {"type": "string"}
					},
					"required": ["parent_chinese", "chinese", "pinyin", "english", "target_word"]
				}
			}
		},
		"required": ["entries", "example_sentences"]
	}`)
}

// enrichResponseSchema is the schema for the Enrich call — takes indexed
// inputs, refers to them by source_index in the reply.
func enrichResponseSchema() json.RawMessage {
	return mustSchema(`{
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"entries": {
				"type": "array",
				"items": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"source_index":         {"type": "integer"},
						"pinyin":               {"type": "string"},
						"english":              {"type": "string"},
						"kind_correction":      {"type": "string", "enum": ["word", "phrase", "verb", "sentence", "unchanged"]},
						"suggested_cloze_word": {"type": ["string", "null"]},
						"grammar_note":         {"type": ["string", "null"]},
						"typo_correction":      {"type": ["string", "null"]}
					},
					"required": ["source_index", "pinyin", "english", "kind_correction", "suggested_cloze_word", "grammar_note", "typo_correction"]
				}
			},
			"example_sentences": {
				"type": "array",
				"items": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"source_index": {"type": "integer"},
						"chinese":      {"type": "string"},
						"pinyin":       {"type": "string"},
						"english":      {"type": "string"},
						"target_word":  {"type": "string"}
					},
					"required": ["source_index", "chinese", "pinyin", "english", "target_word"]
				}
			}
		},
		"required": ["entries", "example_sentences"]
	}`)
}

// mustSchema validates that a schema literal is well-formed JSON at boot.
// Panics on a typo — better than shipping broken schemas.
func mustSchema(s string) json.RawMessage {
	var probe any
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		panic("llm: bad response schema literal: " + err.Error())
	}
	return json.RawMessage(s)
}
