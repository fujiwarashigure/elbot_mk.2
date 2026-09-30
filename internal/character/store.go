// Package character implements the folder-based character library.
//
// Layout:
//
//	<root>/<id>/character.toml   metadata + image index
//	<root>/<id>/profile.md       persona
//	<root>/<id>/world.md         background
//	<root>/<id>/greeting.md      opening line
//	<root>/<id>/examples.md      few-shot examples
//	<root>/<id>/notes/*.md       free-form notes
//	<root>/<id>/images/*         original images
package character

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	FileName     = "character.toml"
	ImagesDir    = "images"
	NotesDir     = "notes"
	scanCooldown = 3 * time.Second

	// Limits for user-writable character data.
	MaxDocBytes           = 128 << 10
	MaxDocsPerCharacter   = 64
	MaxImagesPerCharacter = 32
	MaxNameRunes          = 128
	MaxDescriptionRunes   = 500
	MaxListEntries        = 20
	MaxListEntryRunes     = 64
)

var (
	ErrNotFound  = errors.New("character not found")
	ErrForbidden = errors.New("character is not accessible")
	ErrConflict  = errors.New("character already exists")
	ErrInvalidID = errors.New("invalid character id")
)

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	namePattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}_.-]{0,63}$`)
)

// Visibility controls who can read a character.
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

// Viewer is the actor asking for a character.
type Viewer struct {
	Platform   string
	ActorID    string
	Superadmin bool
}

// Image is one stored character image.
type Image struct {
	Name      string `toml:"name" json:"name"`
	MIMEType  string `toml:"mime_type,omitempty" json:"mime_type,omitempty"`
	MediaID   string `toml:"media_id,omitempty" json:"media_id,omitempty"`
	Path      string `toml:"path,omitempty" json:"path,omitempty"`
	Size      int64  `toml:"size,omitempty" json:"size,omitempty"`
	CreatedAt string `toml:"created_at,omitempty" json:"created_at,omitempty"`
}

// ImageSettings controls how the character is rendered by image_generate.
type ImageSettings struct {
	PresetPrompt   string   `toml:"preset_prompt,omitempty"`
	NegativePrompt string   `toml:"negative_prompt,omitempty"`
	Size           string   `toml:"size,omitempty"`
	Quality        string   `toml:"quality,omitempty"`
	References     []string `toml:"references,omitempty"`
}

// Character is one parsed character folder.
type Character struct {
	ID            string
	Name          string
	Aliases       []string
	Description   string
	Tags          []string
	OwnerPlatform string
	OwnerID       string
	Visibility    Visibility
	CreatedAt     string
	UpdatedAt     string
	Dir           string
	Images        []Image
	Image         ImageSettings
	Docs          map[string]string
}

// WriteRequest updates an existing character or creates a new one.
// A nil pointer inside Docs leaves that document unchanged; a pointer to an
// empty string removes it. RemoveDocs deletes whole documents.
type WriteRequest struct {
	ID            string
	Name          string
	Aliases       []string
	Description   *string
	Tags          []string
	Visibility    string
	OwnerPlatform string
	OwnerID       string
	Docs          map[string]*string
	RemoveDocs    []string
	Image         *ImageSettings
}

// SearchResult is one hit of a full-library search.
type SearchResult struct {
	ID      string
	Name    string
	Tags    []string
	Snippet string
	Score   int
}

type characterMeta struct {
	ID            string   `toml:"id"`
	Name          string   `toml:"name"`
	Aliases       []string `toml:"aliases,omitempty"`
	Description   string   `toml:"description,omitempty"`
	Tags          []string `toml:"tags,omitempty"`
	OwnerPlatform string   `toml:"owner_platform,omitempty"`
	OwnerID       string   `toml:"owner_id,omitempty"`
	Visibility    string   `toml:"visibility,omitempty"`
	CreatedAt     string   `toml:"created_at,omitempty"`
	UpdatedAt     string   `toml:"updated_at,omitempty"`
}

type fileFormat struct {
	Character characterMeta `toml:"character"`
	Image     ImageSettings `toml:"image,omitempty"`
	Images    []Image       `toml:"images"`
}

// Store is a directory-backed, cached character library.
type Store struct {
	Root string

	mu      sync.Mutex
	loaded  time.Time
	entries map[string]*Character
	order   []string
}

// NewStore returns a store rooted at root.
func NewStore(root string) *Store {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "characters"
	}
	return &Store{Root: filepath.Clean(root), entries: map[string]*Character{}}
}

// Enabled reports whether the store is usable.
func (s *Store) Enabled() bool {
	return s != nil && strings.TrimSpace(s.Root) != ""
}

// Refresh forces a full rescan of the root directory.
func (s *Store) Refresh(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanLocked(ctx)
}

func (s *Store) ensure(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded.IsZero() && time.Since(s.loaded) < scanCooldown {
		return nil
	}
	return s.scanLocked(ctx)
}

func (s *Store) scanLocked(ctx context.Context) error {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return fmt.Errorf("create character root: %w", err)
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return fmt.Errorf("read character root: %w", err)
	}
	next := map[string]*Character{}
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || !idPattern.MatchString(entry.Name()) {
			continue
		}
		item, err := readCharacter(filepath.Join(s.Root, entry.Name()))
		if err != nil {
			continue
		}
		next[item.ID] = item
		order = append(order, item.ID)
	}
	sort.Strings(order)
	s.entries = next
	s.order = order
	s.loaded = time.Now()
	return nil
}

func readCharacter(dir string) (*Character, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	var file fileFormat
	if err := toml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", FileName, err)
	}
	meta := file.Character
	if !idPattern.MatchString(meta.ID) {
		return nil, fmt.Errorf("%s: invalid id %q", FileName, meta.ID)
	}
	item := &Character{
		ID:            meta.ID,
		Name:          strings.TrimSpace(meta.Name),
		Aliases:       cleanStrings(meta.Aliases),
		Description:   strings.TrimSpace(meta.Description),
		Tags:          cleanStrings(meta.Tags),
		OwnerPlatform: strings.TrimSpace(meta.OwnerPlatform),
		OwnerID:       strings.TrimSpace(meta.OwnerID),
		Visibility:    normalizeVisibility(meta.Visibility),
		CreatedAt:     strings.TrimSpace(meta.CreatedAt),
		UpdatedAt:     strings.TrimSpace(meta.UpdatedAt),
		Dir:           dir,
		Images:        append([]Image(nil), file.Images...),
		Image:         file.Image,
		Docs:          map[string]string{},
	}
	if item.Name == "" {
		item.Name = item.ID
	}
	for _, kind := range docOrder {
		name, ok := docFileName(kind)
		if !ok {
			continue
		}
		if content, err := readDoc(dir, name); err == nil && strings.TrimSpace(content) != "" {
			item.Docs[kind] = content
		}
	}
	notesDir := filepath.Join(dir, NotesDir)
	if notes, err := os.ReadDir(notesDir); err == nil {
		for _, entry := range notes {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
				continue
			}
			base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			if !namePattern.MatchString(base) {
				continue
			}
			if content, err := readDoc(dir, filepath.ToSlash(filepath.Join(NotesDir, entry.Name()))); err == nil && strings.TrimSpace(content) != "" {
				item.Docs[NotesDir+"/"+base] = content
			}
		}
	}
	sort.Slice(item.Images, func(i, j int) bool { return item.Images[i].Name < item.Images[j].Name })
	return item, nil
}

func readDoc(dir, rel string) (string, error) {
	path, err := safeJoin(dir, rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func safeJoin(dir, rel string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(absDir, filepath.FromSlash(rel))
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if absPath != absDir && !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes character directory", rel)
	}
	return absPath, nil
}

// VisibleTo reports whether viewer may read the character.
func (c *Character) VisibleTo(viewer Viewer) bool {
	if c == nil {
		return false
	}
	if c.Visibility != VisibilityPrivate {
		return true
	}
	if viewer.Superadmin {
		return true
	}
	return viewer.ActorID != "" && c.OwnerID != "" && c.OwnerPlatform == viewer.Platform && c.OwnerID == viewer.ActorID
}

// ManageableBy reports whether viewer may write or delete the character.
func (c *Character) ManageableBy(viewer Viewer) bool {
	if c == nil {
		return false
	}
	if viewer.Superadmin {
		return true
	}
	if viewer.ActorID == "" || c.OwnerID == "" {
		return false
	}
	return c.OwnerPlatform == viewer.Platform && c.OwnerID == viewer.ActorID
}

// List returns the characters visible to viewer, sorted by id.
func (s *Store) List(ctx context.Context, viewer Viewer) ([]*Character, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Character, 0, len(s.order))
	for _, id := range s.order {
		item := s.entries[id]
		if item.VisibleTo(viewer) {
			out = append(out, item)
		}
	}
	return out, nil
}

// Get returns one character by id regardless of visibility.
func (s *Store) Get(ctx context.Context, id string) (*Character, error) {
	id = normalizeID(id)
	if !idPattern.MatchString(id) {
		return nil, ErrInvalidID
	}
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.entries[id]
	if !ok {
		return nil, ErrNotFound
	}
	return item, nil
}

// GetVisible returns one character if viewer may read it.
func (s *Store) GetVisible(ctx context.Context, id string, viewer Viewer) (*Character, error) {
	item, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !item.VisibleTo(viewer) {
		return nil, ErrForbidden
	}
	return item, nil
}

// Prompt renders the injectable persona block.
func (c *Character) Prompt(maxRunes int) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("[角色设定]\n")
	b.WriteString("id: " + c.ID + "\n")
	b.WriteString("name: " + c.Name)
	if len(c.Aliases) > 0 {
		b.WriteString("\naliases: " + strings.Join(c.Aliases, ", "))
	}
	if c.Description != "" {
		b.WriteString("\ndescription: " + c.Description)
	}
	if len(c.Tags) > 0 {
		b.WriteString("\ntags: " + strings.Join(c.Tags, ", "))
	}
	for _, kind := range docOrder {
		content := strings.TrimSpace(c.Docs[kind])
		if content == "" {
			continue
		}
		b.WriteString("\n\n## " + kind + "\n" + content)
	}
	text := strings.TrimSpace(b.String())
	if maxRunes > 0 && len([]rune(text)) > maxRunes {
		text = string([]rune(text)[:maxRunes]) + "\n...[角色设定过长，已截断]"
	}
	return text
}

type activeContextKey struct{}

// WithActive records the character ids applied to the current turn so tools can
// pick them up without relying on the model to repeat the id.
func WithActive(ctx context.Context, ids ...string) context.Context {
	if ctx == nil || len(ids) == 0 {
		return ctx
	}
	current := ActiveIDs(ctx)
	seen := map[string]bool{}
	for _, id := range current {
		seen[id] = true
	}
	for _, id := range ids {
		id = normalizeID(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		current = append(current, id)
	}
	return context.WithValue(ctx, activeContextKey{}, current)
}

// ActiveIDs returns the character ids applied to the current turn.
func ActiveIDs(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	ids, _ := ctx.Value(activeContextKey{}).([]string)
	return append([]string(nil), ids...)
}

// ReadImage loads one stored character image by name.
func (s *Store) ReadImage(ctx context.Context, id, name string, viewer Viewer) ([]byte, string, error) {
	item, err := s.GetVisible(ctx, id, viewer)
	if err != nil {
		return nil, "", err
	}
	name = safeImageName(name)
	if name == "" {
		return nil, "", fmt.Errorf("invalid image name")
	}
	for _, image := range item.Images {
		if image.Name != name {
			continue
		}
		path, err := safeJoin(item.Dir, image.Path)
		if err != nil {
			return nil, "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		return data, image.MIMEType, nil
	}
	return nil, "", ErrNotFound
}

// ImagePrompt returns the effective image preset for the character.
func (c *Character) ImagePrompt() string {
	if c == nil {
		return ""
	}
	parts := []string{}
	if strings.TrimSpace(c.Image.PresetPrompt) != "" {
		parts = append(parts, strings.TrimSpace(c.Image.PresetPrompt))
	}
	if strings.TrimSpace(c.Docs["image_prompt"]) != "" {
		parts = append(parts, strings.TrimSpace(c.Docs["image_prompt"]))
	}
	return strings.Join(parts, "\n\n")
}
