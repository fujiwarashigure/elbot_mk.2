package character

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

var docOrder = []string{"profile", "world", "greeting", "examples", "image_prompt"}

// Write creates or updates a character.
func (s *Store) Write(ctx context.Context, req WriteRequest, viewer Viewer) (*Character, error) {
	req.ID = normalizeID(req.ID)
	if !idPattern.MatchString(req.ID) {
		return nil, ErrInvalidID
	}
	if !s.Enabled() {
		return nil, fmt.Errorf("character library is not configured")
	}
	if err := validateWriteRequest(req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.scanLocked(ctx); err != nil {
		return nil, err
	}
	existing := s.entries[req.ID]
	if existing != nil && !existing.ManageableBy(viewer) {
		return nil, ErrForbidden
	}

	meta := characterMeta{ID: req.ID}
	images := []Image(nil)
	imageSettings := ImageSettings{}
	createdAt := nowString()
	if existing != nil {
		file := existing.toFile()
		meta = file.Character
		images = file.Images
		imageSettings = file.Image
		createdAt = meta.CreatedAt
	} else {
		meta.Visibility = string(VisibilityPublic)
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		meta.Name = name
	}
	if meta.Name == "" {
		meta.Name = req.ID
	}
	if req.Aliases != nil {
		meta.Aliases = cleanStrings(req.Aliases)
	}
	if req.Description != nil {
		meta.Description = strings.TrimSpace(*req.Description)
	}
	if req.Tags != nil {
		meta.Tags = cleanStrings(req.Tags)
	}
	if strings.TrimSpace(req.Visibility) != "" {
		meta.Visibility = string(normalizeVisibility(req.Visibility))
	}
	if req.Image != nil {
		imageSettings = *req.Image
	}
	if req.Version != nil {
		meta.Version = strings.TrimSpace(*req.Version)
	} else if existing != nil {
		meta.Version = nextVersion(meta.Version)
	} else if strings.TrimSpace(meta.Version) == "" {
		meta.Version = "1"
	}
	if req.Source != nil {
		meta.Source = strings.TrimSpace(*req.Source)
	} else if strings.TrimSpace(meta.Source) == "" {
		meta.Source = firstNonEmpty(strings.TrimSpace(viewer.Platform), "local")
	}
	if !viewer.Superadmin {
		// Regular users own their characters and can only keep them private.
		meta.OwnerPlatform = viewer.Platform
		meta.OwnerID = viewer.ActorID
		meta.Visibility = string(VisibilityPrivate)
	} else if existing == nil {
		meta.OwnerPlatform = strings.TrimSpace(req.OwnerPlatform)
		meta.OwnerID = strings.TrimSpace(req.OwnerID)
	}
	meta.CreatedAt = firstNonEmpty(createdAt, nowString())
	meta.UpdatedAt = nowString()

	dir := filepath.Join(s.Root, req.ID)
	if err := os.MkdirAll(filepath.Join(dir, NotesDir), 0o755); err != nil {
		return nil, fmt.Errorf("create character directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ImagesDir), 0o755); err != nil {
		return nil, fmt.Errorf("create character images directory: %w", err)
	}
	for _, kind := range req.RemoveDocs {
		if name, ok := docFileName(kind); ok {
			_ = os.Remove(filepath.Join(dir, filepath.FromSlash(name)))
		}
	}
	for kind, value := range req.Docs {
		if value == nil {
			continue
		}
		name, ok := docFileName(kind)
		if !ok {
			return nil, fmt.Errorf("invalid character document %q", kind)
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		if strings.TrimSpace(*value) == "" {
			_ = os.Remove(path)
			continue
		}
		if err := writeFileAtomic(path, []byte(strings.TrimSpace(*value)+"\n")); err != nil {
			return nil, err
		}
	}
	if err := writeToml(dir, fileFormat{Character: meta, Image: imageSettings, Images: images}); err != nil {
		return nil, err
	}
	if err := s.scanLocked(ctx); err != nil {
		return nil, err
	}
	return s.entries[req.ID], nil
}

// Delete removes a character folder permanently.
func (s *Store) Delete(ctx context.Context, id string, viewer Viewer) error {
	item, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if !item.ManageableBy(viewer) {
		return ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(s.Root, item.ID)); err != nil {
		return err
	}
	return s.scanLocked(ctx)
}

// AddImage stores the original bytes plus the Media Center reference.
func (s *Store) AddImage(ctx context.Context, id, name, mimeType, mediaID string, data []byte, viewer Viewer) (*Image, error) {
	return s.AddImageWithMeta(ctx, id, name, mimeType, mediaID, "", "", data, viewer)
}

// AddImageWithMeta is AddImage plus explicit asset version and source label.
func (s *Store) AddImageWithMeta(ctx context.Context, id, name, mimeType, mediaID, version, source string, data []byte, viewer Viewer) (*Image, error) {
	name = safeImageName(name)
	if name == "" {
		return nil, fmt.Errorf("invalid image name")
	}
	item, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !item.ManageableBy(viewer) {
		return nil, ErrForbidden
	}
	if len(item.Images) >= MaxImagesPerCharacter {
		exists := false
		for _, image := range item.Images {
			if image.Name == name {
				exists = true
				break
			}
		}
		if !exists {
			return nil, fmt.Errorf("每个角色最多 %d 张图片", MaxImagesPerCharacter)
		}
	}
	imagesDir := filepath.Join(item.Dir, ImagesDir)
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(imagesDir, name), data); err != nil {
		return nil, err
	}
	image := Image{
		Name:      name,
		MIMEType:  strings.TrimSpace(mimeType),
		MediaID:   strings.TrimSpace(mediaID),
		Path:      filepath.ToSlash(filepath.Join(ImagesDir, name)),
		Size:      int64(len(data)),
		Version:   firstNonEmpty(strings.TrimSpace(version), "1"),
		Source:    firstNonEmpty(strings.TrimSpace(source), strings.TrimSpace(viewer.Platform), "local"),
		CreatedAt: nowString(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.scanLocked(ctx); err != nil {
		return nil, err
	}
	current := s.entries[item.ID]
	if current == nil {
		return nil, ErrNotFound
	}
	file := current.toFile()
	replaced := false
	for i := range file.Images {
		if file.Images[i].Name == name {
			file.Images[i] = image
			replaced = true
		}
	}
	if !replaced {
		file.Images = append(file.Images, image)
	}
	sort.Slice(file.Images, func(i, j int) bool { return file.Images[i].Name < file.Images[j].Name })
	file.Character.UpdatedAt = nowString()
	if err := writeToml(item.Dir, file); err != nil {
		return nil, err
	}
	if err := s.scanLocked(ctx); err != nil {
		return nil, err
	}
	return &image, nil
}

// RemoveImage deletes one stored image file and its index entry.
func (s *Store) RemoveImage(ctx context.Context, id, name string, viewer Viewer) error {
	item, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if !item.ManageableBy(viewer) {
		return ErrForbidden
	}
	name = safeImageName(name)
	if name == "" {
		return fmt.Errorf("invalid image name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.scanLocked(ctx); err != nil {
		return err
	}
	current := s.entries[item.ID]
	if current == nil {
		return ErrNotFound
	}
	file := current.toFile()
	kept := file.Images[:0]
	for _, image := range file.Images {
		if image.Name == name {
			continue
		}
		kept = append(kept, image)
	}
	file.Images = kept
	file.Character.UpdatedAt = nowString()
	if err := writeToml(item.Dir, file); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(item.Dir, ImagesDir, name))
	return s.scanLocked(ctx)
}

func (c *Character) toFile() fileFormat {
	return fileFormat{
		Character: characterMeta{
			ID:            c.ID,
			Name:          c.Name,
			Aliases:       append([]string(nil), c.Aliases...),
			Description:   c.Description,
			Tags:          append([]string(nil), c.Tags...),
			OwnerPlatform: c.OwnerPlatform,
			OwnerID:       c.OwnerID,
			Visibility:    string(c.Visibility),
			Version:       c.Version,
			Source:        c.Source,
			CreatedAt:     c.CreatedAt,
			UpdatedAt:     c.UpdatedAt,
		},
		Image:  c.Image,
		Images: append([]Image(nil), c.Images...),
	}
}

func writeToml(dir string, file fileFormat) error {
	data, err := toml.Marshal(file)
	if err != nil {
		return fmt.Errorf("encode %s: %w", FileName, err)
	}
	return writeFileAtomic(filepath.Join(dir, FileName), data)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".character-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func docFileName(kind string) (string, bool) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case "profile", "world", "greeting", "examples", "image_prompt":
		return kind + ".md", true
	}
	kind = strings.TrimPrefix(kind, NotesDir+"/")
	if namePattern.MatchString(kind) {
		return filepath.ToSlash(filepath.Join(NotesDir, kind+".md")), true
	}
	return "", false
}

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func normalizeVisibility(value string) Visibility {
	switch Visibility(strings.ToLower(strings.TrimSpace(value))) {
	case VisibilityPrivate:
		return VisibilityPrivate
	default:
		return VisibilityPublic
	}
}

func safeImageName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == string(filepath.Separator) || strings.HasPrefix(name, ".") {
		return ""
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 {
			return ""
		}
	}
	return name
}

func validateWriteRequest(req WriteRequest) error {
	if runes := len([]rune(strings.TrimSpace(req.Name))); runes > MaxNameRunes {
		return fmt.Errorf("角色名称最多 %d 字符", MaxNameRunes)
	}
	if req.Description != nil {
		if runes := len([]rune(strings.TrimSpace(*req.Description))); runes > MaxDescriptionRunes {
			return fmt.Errorf("角色简介最多 %d 字符", MaxDescriptionRunes)
		}
	}
	for _, list := range [][]string{req.Aliases, req.Tags} {
		if len(list) > MaxListEntries {
			return fmt.Errorf("别名 / tags 每项最多 %d 个", MaxListEntries)
		}
		for _, value := range list {
			if runes := len([]rune(strings.TrimSpace(value))); runes > MaxListEntryRunes {
				return fmt.Errorf("别名 / tag 单项最多 %d 字符", MaxListEntryRunes)
			}
		}
	}
	count := 0
	for _, value := range req.Docs {
		if value == nil {
			continue
		}
		count++
		if len(*value) > MaxDocBytes {
			return fmt.Errorf("单个文档最多 %d bytes", MaxDocBytes)
		}
	}
	if count > MaxDocsPerCharacter {
		return fmt.Errorf("每个角色最多 %d 个文档", MaxDocsPerCharacter)
	}
	return nil
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func nextVersion(current string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return "1"
	}
	value, err := strconv.Atoi(current)
	if err != nil || value <= 0 {
		return "1"
	}
	return strconv.Itoa(value + 1)
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
