package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

// Library icons are a fixed client vocabulary: every client compiles in one
// symbol per name. The names describe a shape or a medium, never a plugin or
// a domain, so core stays domain-agnostic and an owner can give any Library
// any icon. Adding a name is an additive wire change: a client that does not
// know it falls back to its own placeholder.
const (
	LibraryIconFilm       = "film"
	LibraryIconTV         = "tv"
	LibraryIconVideo      = "video"
	LibraryIconNote       = "note"
	LibraryIconMic        = "mic"
	LibraryIconBroadcast  = "broadcast"
	LibraryIconHeadphones = "headphones"
	LibraryIconBook       = "book"
	LibraryIconImage      = "image"
	LibraryIconCamera     = "camera"
	LibraryIconPeople     = "people"
	LibraryIconStar       = "star"
	LibraryIconHeart      = "heart"
	LibraryIconSparkles   = "sparkles"
	LibraryIconLayers     = "layers"
	LibraryIconFolder     = "folder"
)

var libraryIcons = []string{
	LibraryIconFilm,
	LibraryIconTV,
	LibraryIconVideo,
	LibraryIconNote,
	LibraryIconMic,
	LibraryIconBroadcast,
	LibraryIconHeadphones,
	LibraryIconBook,
	LibraryIconImage,
	LibraryIconCamera,
	LibraryIconPeople,
	LibraryIconStar,
	LibraryIconHeart,
	LibraryIconSparkles,
	LibraryIconLayers,
	LibraryIconFolder,
}

// LibraryIcons returns the icon vocabulary in its presentation order.
func LibraryIcons() []string {
	return slices.Clone(libraryIcons)
}

// IsLibraryIcon reports whether name is in the icon vocabulary.
func IsLibraryIcon(name string) bool {
	return slices.Contains(libraryIcons, name)
}

// DefaultLibraryIcon picks the icon for a Library whose owner has not chosen
// one, from the media classes its Plugin declares. Media class is all core
// knows about a Plugin's content: two Plugins of the same class get the same
// default until the owner picks another.
func DefaultLibraryIcon(mediaClasses []string) string {
	var video, audio, image bool
	for _, class := range mediaClasses {
		switch MediaClass(class) {
		case MediaClassVideo:
			video = true
		case MediaClassAudio:
			audio = true
		case MediaClassImage:
			image = true
		}
	}
	switch {
	case video && !audio && !image:
		return LibraryIconFilm
	case audio && !video && !image:
		return LibraryIconNote
	case image && !video && !audio:
		return LibraryIconImage
	case video || audio || image:
		return LibraryIconLayers
	default:
		return LibraryIconFolder
	}
}

// EffectiveIcon returns the owner's choice when it is still in the
// vocabulary, otherwise the default for the given media classes.
func (l Library) EffectiveIcon(mediaClasses []string) string {
	if IsLibraryIcon(l.Icon) {
		return l.Icon
	}
	return DefaultLibraryIcon(mediaClasses)
}

// libraryIconConfigPrefix keys the owner's icon choice per Library in
// server_config. An absent row means the Library follows its default, which
// is the state of every Library created before icons existed. The choice sits
// in the settings table rather than a library column so that a presentation
// preference does not move the frozen catalog schema.
const libraryIconConfigPrefix = "library.icon."

// SetLibraryIcon stores the owner's icon choice for a Library. An empty icon
// clears the choice so the Library follows its default again.
func (s *Store) SetLibraryIcon(ctx context.Context, id LibraryID, icon string, now time.Time) error {
	if id == "" {
		return errors.New("library ID is required")
	}
	if icon != "" && !IsLibraryIcon(icon) {
		return ValidationError("unknown library icon")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM library WHERE id = ? AND trashed_at_ms IS NULL", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("library not found")
	}
	if err != nil {
		return err
	}
	key := libraryIconConfigPrefix + string(id)
	if icon == "" {
		_, err = tx.ExecContext(ctx, "DELETE FROM server_config WHERE key = ?", key)
	} else {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO server_config(key, value, updated_at_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET
				value = excluded.value,
				updated_at_ms = excluded.updated_at_ms`,
			key, icon, timeToMillis(now))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
