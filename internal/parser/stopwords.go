package parser

// ChineseStopwords are tokens to skip when picking a cloze target.
// For Chinese, this matters mostly when a sentence is segmented by spaces
// (which the LLM-pipeline does — example sentences come back with spaces or
// pinyin tokens). For unsegmented hanzi we rarely pick by token; the LLM
// supplies `suggested_cloze_word` instead.
var ChineseStopwords = map[string]bool{
	// Pronouns
	"我": true, "你": true, "他": true, "她": true, "它": true,
	"我们": true, "你们": true, "他们": true, "她们": true, "它们": true,
	"咱们": true,
	// Particles / aspect markers
	"的": true, "了": true, "着": true, "过": true, "吗": true, "呢": true, "吧": true, "啊": true, "呀": true,
	// Common verbs/copulas/modals
	"是": true, "有": true, "在": true, "会": true, "要": true, "能": true, "可以": true,
	// Connectives & prepositions
	"和": true, "跟": true, "与": true, "或": true, "但": true, "但是": true,
	"从": true, "到": true, "把": true, "被": true, "对": true, "向": true, "给": true,
	// Demonstratives, measure-word fillers
	"这": true, "那": true, "这个": true, "那个": true, "一个": true, "一": true,
	// Belt-and-braces if English leaks in
	"this": true, "that": true,
}

// chinesePronouns help detect sentence-ness when classifying a bare line.
var chinesePronouns = map[string]bool{
	"我": true, "你": true, "他": true, "她": true, "它": true,
	"我们": true, "你们": true, "他们": true, "她们": true, "咱们": true,
}

// auxiliary/copula/modal verbs that mark sentence-ness even without pronoun
var chineseVerbs = map[string]bool{
	"是": true, "有": true, "在": true, "会": true, "要": true, "能": true, "可以": true, "想": true,
}
