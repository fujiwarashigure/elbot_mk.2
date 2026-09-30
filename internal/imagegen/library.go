package imagegen

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed prompts/library.json
var promptLibraryJSON []byte

// PromptTemplate is one row of the 万能模板 sheet.
type PromptTemplate struct {
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	Scene     string   `json:"scene"`
	Template  string   `json:"template"`
	Variables []string `json:"variables"`
}

// PromptEntry is one row of the 全部提示词汇总 sheet.
type PromptEntry struct {
	Category    string   `json:"category"`
	Scene       string   `json:"scene"`
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Variables   []string `json:"variables"`
	Anchors     []string `json:"anchors"`
	Terms       []string `json:"terms"`
	Keys        []string `json:"keys"`
	GenericKeys []string `json:"generic_keys"`
}

// NegativeGroup is one row of the 负面提示词 sheet.
type NegativeGroup struct {
	Key      string   `json:"key"`
	Category string   `json:"category"`
	Items    []string `json:"items"`
	Terms    []string `json:"terms"`
}

// TermCount is one entry of the word-level vocabulary.
type TermCount struct {
	Word  string `json:"word"`
	Count int    `json:"count"`
}

// UseCasePreset is one row of the 参数速查 sheet.
type UseCasePreset struct {
	Use        string   `json:"use"`
	Ratio      string   `json:"ratio"`
	Scene      string   `json:"scene"`
	Supplement string   `json:"supplement"`
	Keys       []string `json:"keys"`
	SceneKeys  []string `json:"scene_keys"`
}

// PromptTip is one row of the 进阶技巧 sheet.
type PromptTip struct {
	Module  string `json:"module"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Example string `json:"example"`
}

// PromptLibrary is the embedded prompt optimization knowledge base.
type PromptLibrary struct {
	Version      int                 `json:"version"`
	Source       string              `json:"source"`
	Templates    []PromptTemplate    `json:"templates"`
	Entries      []PromptEntry       `json:"entries"`
	Keywords     map[string][]string `json:"keywords"`
	KeywordTerms map[string][]string `json:"keyword_terms"`
	Negatives    []NegativeGroup     `json:"negatives"`
	Presets      []UseCasePreset     `json:"presets"`
	Tips         []PromptTip         `json:"tips"`
	Vocabulary   []TermCount         `json:"vocabulary"`

	vocabIndex map[string]int
}

var (
	promptLibraryOnce  sync.Once
	promptLibraryValue *PromptLibrary
	promptLibraryErr   error
)

// PromptLib returns the embedded prompt library, loading it once.
func PromptLib() (*PromptLibrary, error) {
	promptLibraryOnce.Do(func() {
		var library PromptLibrary
		if err := json.Unmarshal(promptLibraryJSON, &library); err != nil {
			promptLibraryErr = fmt.Errorf("parse embedded prompt library: %w", err)
			return
		}
		if len(library.Entries) == 0 {
			promptLibraryErr = fmt.Errorf("embedded prompt library is empty")
			return
		}
		library.vocabIndex = make(map[string]int, len(library.Vocabulary))
		for _, item := range library.Vocabulary {
			library.vocabIndex[item.Word] = item.Count
		}
		promptLibraryValue = &library
	})
	return promptLibraryValue, promptLibraryErr
}

// VocabularyCount returns how often a word appears across the library.
func (l *PromptLibrary) VocabularyCount(word string) int {
	if l == nil || l.vocabIndex == nil {
		return 0
	}
	return l.vocabIndex[strings.ToLower(strings.TrimSpace(word))]
}

// KeywordGroup returns one keyword bank by prefix, e.g. "风格".
func (l *PromptLibrary) KeywordGroup(prefix string) []string {
	if l == nil {
		return nil
	}
	for name, items := range l.Keywords {
		if name == prefix || len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			return items
		}
	}
	return nil
}

// NegativeItems returns the item list for one negative group key.
func (l *PromptLibrary) NegativeItems(key string) []string {
	if l == nil {
		return nil
	}
	for _, group := range l.Negatives {
		if group.Key == key {
			return group.Items
		}
	}
	return nil
}
