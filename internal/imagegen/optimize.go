package imagegen

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// OptimizeOptions controls how much the rules optimizer may add.
type OptimizeOptions struct {
	MaxAnchors    int
	MaxNegatives  int
	MaxAddedRunes int
	MaxTags       int
	// TermMode is "phrase" (default) or "tag". Tag mode appends a compact
	// comma-separated single-word tag string instead of phrase anchors.
	TermMode string
}

// OptimizeResult is the rules optimizer output.
type OptimizeResult struct {
	Prompt   string
	Negative string
	Matched  string
	Anchors  []string
	Tags     []string
}

var ratioPattern = regexp.MustCompile(`\b\d{1,2}\s*[:：]\s*\d{1,2}\b`)

var negativeCaps = map[string]int{
	"quality":     3,
	"exposure":    2,
	"safety":      3,
	"anatomy":     2,
	"face":        2,
	"duplication": 1,
	"text":        3,
	"composition": 2,
	"perspective": 1,
	"style":       2,
}

// Optimize rewrites a user scene description using the embedded prompt library:
// it detects the use case / category, appends targeted anchors and ratio, and
// selects a small set of relevant negative prompts.
func (l *PromptLibrary) Optimize(scene string, opts OptimizeOptions) OptimizeResult {
	original := strings.TrimSpace(scene)
	result := OptimizeResult{Prompt: original}
	if l == nil || original == "" {
		return result
	}
	if opts.MaxAnchors <= 0 {
		opts.MaxAnchors = 4
	}
	if opts.MaxNegatives <= 0 {
		opts.MaxNegatives = 10
	}
	folded := strings.ToLower(original)
	if strings.EqualFold(strings.TrimSpace(opts.TermMode), "tag") {
		return l.optimizeTags(original, folded, opts)
	}

	added := []string{}
	if preset, ok := l.matchPreset(folded); ok {
		result.Matched = "use=" + preset.Use
		if !hasAspectRatio(original) {
			if ratio := ratioText(preset.Ratio); ratio != "" {
				added = append(added, ratio)
			}
		}
		if supplement := strings.TrimSpace(preset.Supplement); supplement != "" && !containsFold(folded, supplement) {
			added = append(added, supplement)
		}
	}

	anchors := []string{}
	matchedEntry, entryOK, allowAnchors := l.matchEntry(folded)
	entry := matchedEntry
	if entryOK {
		if result.Matched != "" {
			result.Matched += "; "
		}
		result.Matched += "category=" + entry.Category + "/" + entry.Scene
		if !allowAnchors {
			entry.Anchors = nil
		}
		for _, anchor := range entry.Anchors {
			if len(anchors) >= opts.MaxAnchors {
				break
			}
			if !usefulAnchor(anchor) || containsFold(folded, anchor) || containsAny(added, anchor) {
				continue
			}
			anchors = append(anchors, anchor)
		}
	}
	if entryOK && allowAnchors && len(anchors) < opts.MaxAnchors {
		extra := 0
		for _, term := range matchableTerms(matchedEntry) {
			if len(anchors) >= opts.MaxAnchors || extra >= 2 {
				break
			}
			if containsFold(folded, term) || containsAny(added, term) || containsAny(anchors, term) {
				continue
			}
			anchors = append(anchors, term)
			extra++
		}
	}
	if len(anchors) == 0 {
		if quality := l.KeywordGroup("画质"); len(quality) > 0 {
			for _, keyword := range quality {
				if containsFold(folded, keyword) {
					continue
				}
				anchors = append(anchors, keyword)
				break
			}
		}
	}
	if len(anchors) > 0 {
		added = append(added, anchors...)
		result.Anchors = anchors
	}
	added = dedupeFold(added)
	if editTrigger(folded) {
		added = append(added, "Keep the subject, pose, composition, lighting, and background unchanged; do not redraw the whole image")
	}
	result.Negative = strings.Join(l.selectNegatives(folded, opts.MaxNegatives), ", ")

	for i := range added {
		added[i] = strings.TrimRight(strings.TrimSpace(added[i]), ".")
	}
	addedText := squashCommas(strings.TrimSpace(strings.Join(added, ", ")))
	if opts.MaxAddedRunes > 0 && len([]rune(addedText)) > opts.MaxAddedRunes {
		addedText = strings.TrimSpace(string([]rune(addedText)[:opts.MaxAddedRunes]))
	}
	if addedText != "" {
		result.Prompt = strings.TrimSpace(original + " " + addedText)
	}
	return result
}

// optimizeTags builds a compact single-word tag string.
func (l *PromptLibrary) optimizeTags(original, folded string, opts OptimizeOptions) OptimizeResult {
	result := OptimizeResult{Prompt: original}
	if opts.MaxTags <= 0 {
		opts.MaxTags = 12
	}
	ratio := ""
	supplement := ""
	if preset, ok := l.matchPreset(folded); ok {
		result.Matched = "use=" + preset.Use
		if !hasAspectRatio(original) {
			ratio = cleanRatio(preset.Ratio)
		}
		supplement = preset.Supplement
	}
	entry, entryOK, allowAnchors := l.matchEntry(folded)
	if entryOK {
		if result.Matched != "" {
			result.Matched += "; "
		}
		result.Matched += "category=" + entry.Category + "/" + entry.Scene
	}

	weights := map[string]int{}
	counts := map[string]int{}
	record := func(word string, weight int) {
		if weight > weights[word] {
			weights[word] = weight
		}
		if counts[word] == 0 {
			counts[word] = l.VocabularyCount(word)
		}
	}
	// Entry terms are already single words.
	if entryOK && allowAnchors {
		added := 0
		for _, term := range entry.Terms {
			if added >= 6 {
				break
			}
			if !validTagWord(term) || containsFold(folded, term) {
				continue
			}
			record(term, 30)
			added++
		}
	}
	// Use-case supplement: keep meaningful words only (drop numbers/short noise).
	if supplement != "" {
		added := 0
		for _, word := range splitTagWords(supplement) {
			if added >= 4 {
				break
			}
			if len([]rune(word)) < 4 || containsFold(folded, word) {
				continue
			}
			record(word, 25)
			added++
		}
	}
	matched := entryOK || strings.TrimSpace(result.Matched) != ""
	if matched || !editTrigger(folded) {
		// Quality tags are style-neutral; other keyword groups are intentionally
		// skipped because a blindly picked "photorealistic" or "backlight" can
		// contradict the matched scene.
		added := 0
		for _, item := range l.KeywordGroup("画质") {
			if added >= 2 {
				break
			}
			if strings.ContainsAny(item, " ") || !validTagWord(item) || containsFold(folded, strings.ToLower(item)) {
				continue
			}
			record(strings.ToLower(strings.TrimSpace(item)), 15)
			added++
		}
	}

	type candidate struct {
		word   string
		weight int
		count  int
	}
	candidates := make([]candidate, 0, len(weights))
	for word, weight := range weights {
		candidates = append(candidates, candidate{word: word, weight: weight, count: counts[word]})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].weight != candidates[j].weight {
			return candidates[i].weight > candidates[j].weight
		}
		if candidates[i].count != candidates[j].count {
			return candidates[i].count > candidates[j].count
		}
		return candidates[i].word < candidates[j].word
	})

	tags := []string{}
	if ratio != "" {
		tags = append(tags, ratio)
	}
	for _, item := range candidates {
		if len(tags) >= opts.MaxTags {
			break
		}
		tags = append(tags, item.word)
	}
	result.Tags = tags

	addedText := strings.Join(tags, ", ")
	if editTrigger(folded) {
		guard := "Keep the subject, pose, composition, lighting, and background unchanged; do not redraw the whole image"
		if addedText == "" {
			addedText = guard
		} else {
			addedText += ", " + guard
		}
	}
	if opts.MaxAddedRunes > 0 && len([]rune(addedText)) > opts.MaxAddedRunes {
		addedText = strings.TrimSpace(string([]rune(addedText)[:opts.MaxAddedRunes]))
	}
	if addedText != "" {
		result.Prompt = strings.TrimSpace(original + " " + addedText)
	}
	result.Negative = strings.Join(l.selectNegatives(folded, opts.MaxNegatives), ", ")
	return result
}

func groupAlreadyPresent(items []string, folded string) bool {
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" && strings.Contains(folded, item) {
			return true
		}
	}
	return false
}

func cleanRatio(ratio string) string {
	ratio = strings.TrimSpace(ratio)
	if ratio == "" {
		return ""
	}
	for _, sep := range []string{"或", "|", "／", "/"} {
		if index := strings.Index(ratio, sep); index > 0 {
			ratio = strings.TrimSpace(ratio[:index])
		}
	}
	return ratio
}

var tagStoplist = map[string]bool{
	"photo": true, "image": true, "picture": true, "style": true,
	"generate": true, "create": true, "using": true, "keep": true,
	"dpi": true, "safe": true, "ready": true,
}

func validTagWord(word string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	if len([]rune(word)) < 3 || tagStoplist[word] {
		return false
	}
	hasLetter := false
	for _, r := range word {
		if unicode.IsLetter(r) {
			hasLetter = true
			continue
		}
		if !unicode.IsDigit(r) && r != '-' {
			return false
		}
	}
	return hasLetter
}

func splitTagWords(text string) []string {
	out := []string{}
	seen := map[string]bool{}
	var current strings.Builder
	flush := func() {
		word := strings.ToLower(strings.Trim(current.String(), "'-"))
		current.Reset()
		if len([]rune(word)) < 3 || tagStoplist[word] || seen[word] || !validTagWord(word) {
			return
		}
		seen[word] = true
		out = append(out, word)
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == 0x27 {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func (l *PromptLibrary) matchPreset(folded string) (UseCasePreset, bool) {
	best := UseCasePreset{}
	bestScore := 0
	for _, preset := range l.Presets {
		for _, key := range preset.Keys {
			if score := matchKeyScore(folded, key, 4); score > bestScore {
				best = preset
				bestScore = score
			}
		}
		for _, key := range preset.SceneKeys {
			if score := matchKeyScore(folded, key, 0); score > bestScore {
				best = preset
				bestScore = score
			}
		}
	}
	return best, bestScore > 0
}

func matchKeyScore(folded, key string, bonus int) int {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" || !strings.Contains(folded, key) {
		return 0
	}
	return len([]rune(key)) + bonus
}

func (l *PromptLibrary) matchEntry(folded string) (PromptEntry, bool, bool) {
	best := PromptEntry{}
	bestScore := 0
	bestAllow := false
	for _, entry := range l.Entries {
		score := 0
		allow := false
		for _, key := range entry.Keys {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" || !strings.Contains(folded, key) {
				continue
			}
			score += len([]rune(key))
			allow = true
		}
		if !allow {
			// Generic keys (头像/海报/主图…) only match when nothing stronger did.
			for _, key := range entry.GenericKeys {
				key = strings.ToLower(strings.TrimSpace(key))
				if key != "" && strings.Contains(folded, key) {
					score += 1
					break
				}
			}
		}
		matchedTerms := 0
		for _, word := range matchableTerms(entry) {
			if matchedTerms >= 3 {
				break
			}
			if !strings.Contains(folded, word) {
				continue
			}
			score += len([]rune(word)) + 2
			matchedTerms++
			allow = true
		}
		if allow && entry.Category != "通用" && score > 0 {
			score++
		}
		if score > bestScore {
			best = entry
			bestScore = score
			bestAllow = allow
		}
	}
	return best, bestScore > 0, bestAllow
}

var anchorWordStoplist = map[string]bool{
	"detailed": true, "realistic": true, "quality": true, "background": true,
	"natural": true, "soft": true, "light": true, "lighting": true, "color": true,
	"colors": true, "style": true, "photo": true, "image": true, "high": true,
	"modern": true, "sharp": true, "focus": true, "texture": true, "clean": true,
	"composition": true, "premium": true, "beautiful": true, "elegant": true,
	"summer": true, "winter": true, "spring": true, "autumn": true,
	"season": true, "seasons": true, "four": true, "panel": true,
	"subject": true, "scene": true, "action": true, "palette": true,
	"aspect": true, "ratio": true, "shape": true, "form": true,
	"object": true, "item": true, "element": true, "part": true,
	"area": true, "shot": true, "depth": true, "field": true,
	"wide": true, "angle": true, "view": true, "medium": true,
	"full": true, "size": true, "type": true, "kind": true,
}

func anchorWords(anchor string) []string {
	out := []string{}
	for _, word := range strings.Fields(strings.ToLower(anchor)) {
		word = strings.Trim(word, ",.;:!?()[]")
		if !matchableWord(word) {
			continue
		}
		out = append(out, word)
	}
	return out
}

// matchableTerms returns the entry's single-word terms plus the words inside its
// phrase anchors, deduped and filtered.
func matchableTerms(entry PromptEntry) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(word string) {
		word = strings.ToLower(strings.Trim(strings.TrimSpace(word), ",.;:!?()[]"))
		if !matchableWord(word) || seen[word] {
			return
		}
		seen[word] = true
		out = append(out, word)
	}
	for _, term := range entry.Terms {
		add(term)
	}
	for _, anchor := range entry.Anchors {
		for _, word := range strings.Fields(anchor) {
			add(word)
		}
	}
	return out
}

func matchableWord(word string) bool {
	return len([]rune(word)) >= 6 && !anchorWordStoplist[word]
}

func (l *PromptLibrary) selectNegatives(folded string, max int) []string {
	keys := []string{"quality"}
	if peopleTrigger(folded) {
		keys = append(keys, "anatomy", "face", "duplication")
	}
	if textTrigger(folded) {
		keys = append(keys, "text")
	}
	if productTrigger(folded) {
		keys = append(keys, "composition")
	}
	if photoTrigger(folded) {
		keys = append(keys, "style", "exposure")
	}
	keys = append(keys, "safety")

	out := []string{}
	seen := map[string]bool{}
	for _, key := range keys {
		cap := negativeCaps[key]
		if cap <= 0 {
			cap = 2
		}
		count := 0
		for _, item := range l.NegativeItems(key) {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			out = append(out, item)
			count++
			if count >= cap {
				break
			}
		}
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

var peopleTriggers = []string{
	"人物", "人像", "头像", "写真", "肖像", "角色", "少女", "女孩", "男孩", "男人", "女人",
	"情侣", "全家", "模特", "肖像照", "catgirl", "portrait", "girl", "boy", "woman", "man",
	"character", "person", "elf", "cyborg", "warrior", "knight", "mage",
}

var textTriggers = []string{
	"文字", "标题", "字体", "海报", "封面", "logo", "ui", "banner", "typography",
	"text", "title", "caption", "label", "headline",
}

var productTriggers = []string{
	"产品", "商品", "电商", "主图", "详情页", "广告", "product", "e-commerce", "packshot",
	"advertisement", "brand", "logo",
}

var photoTriggers = []string{
	"摄影", "写实", "照片", "人像", "photograph", "photo", "realistic", "photorealistic", "editorial",
}

var editTriggers = []string{
	"换背景", "背景换", "替换成", "换成", "换衣", "换发色", "改表情", "局部", "重绘", "扩图", "放大", "修复", "去物体", "去掉", "去除", "移除", "加雪景", "白天变夜",
	"edit this image", "inpaint", "outpaint", "remove ", "replace ", "change the ", "only change",
}

func editTrigger(folded string) bool { return containsAnyKeyword(folded, editTriggers) }

func peopleTrigger(folded string) bool  { return containsAnyKeyword(folded, peopleTriggers) }
func textTrigger(folded string) bool    { return containsAnyKeyword(folded, textTriggers) }
func productTrigger(folded string) bool { return containsAnyKeyword(folded, productTriggers) }
func photoTrigger(folded string) bool   { return containsAnyKeyword(folded, photoTriggers) }

func containsAnyKeyword(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func containsFold(text, sub string) bool {
	sub = strings.ToLower(strings.TrimSpace(sub))
	return sub != "" && strings.Contains(text, sub)
}

func containsAny(values []string, sub string) bool {
	sub = strings.ToLower(strings.TrimSpace(sub))
	if sub == "" {
		return false
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), sub) {
			return true
		}
	}
	return false
}

func hasAspectRatio(text string) bool {
	if ratioPattern.MatchString(text) {
		return true
	}
	folded := strings.ToLower(text)
	for _, word := range []string{"widescreen", "vertical", "square", "landscape", "portrait aspect", "aspect ratio"} {
		if strings.Contains(folded, word) {
			return true
		}
	}
	return false
}

func ratioText(ratio string) string {
	ratio = strings.TrimSpace(ratio)
	switch ratio {
	case "1:1":
		return "1:1 square composition"
	case "3:4", "4:5":
		return ratio + " vertical portrait composition"
	case "2:3":
		return "2:3 vertical composition"
	case "9:16":
		return "9:16 vertical composition"
	case "16:9":
		return "16:9 widescreen composition"
	case "21:9", "2.39:1":
		return ratio + " cinematic widescreen composition"
	case "3:2", "5:4":
		return ratio + " composition"
	case "":
		return ""
	default:
		return ratio + " composition"
	}
}

func usefulAnchor(anchor string) bool {
	anchor = strings.TrimSpace(anchor)
	if len([]rune(anchor)) < 6 {
		return false
	}
	words := strings.Fields(strings.ToLower(anchor))
	if len(words) >= 2 {
		return true
	}
	return len([]rune(anchor)) >= 8
}

func dedupeFold(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func squashCommas(text string) string {
	for strings.Contains(text, ", ,") {
		text = strings.ReplaceAll(text, ", ,", ",")
	}
	text = strings.ReplaceAll(text, " ,", ",")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.Trim(text, " ,")
	return text
}
