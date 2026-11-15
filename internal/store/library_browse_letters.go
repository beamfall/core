package store

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// libraryBrowseOtherInitial is the bucket for titles that do not open with a
// letter: digits, punctuation, symbols and the empty title.
const libraryBrowseOtherInitial = "#"

// LibraryBrowseLetter is where one title initial starts in a title-sorted
// browse: the offset of its first item in the filtered, sorted set, and how
// many items share the initial. A client jumps there by requesting that
// offset, without loading the pages before it.
type LibraryBrowseLetter struct {
	Letter string
	Offset int
	Count  int
}

// libraryBrowseInitial is the jump-rail bucket of a title: its first
// character upper-cased when it is a letter, else "#". It reads the same raw
// title the title sort compares, so a bucket's offsets follow that order.
func libraryBrowseInitial(title string) string {
	r, _ := utf8.DecodeRuneInString(title)
	if r == utf8.RuneError || !unicode.IsLetter(r) {
		return libraryBrowseOtherInitial
	}
	return strings.ToUpper(string(r))
}

// libraryBrowseLetters lists, in order of increasing offset, the first offset
// and item count of each title initial in candidates, which must already be in
// browse order. A bucket the order visits twice (punctuation that sorts after
// "z") keeps its first offset and counts every item.
func libraryBrowseLetters(candidates []libraryBrowseCandidate) []LibraryBrowseLetter {
	letters := []LibraryBrowseLetter{}
	at := map[string]int{}
	for offset, c := range candidates {
		initial := libraryBrowseInitial(c.titleKey())
		if i, ok := at[initial]; ok {
			letters[i].Count++
			continue
		}
		at[initial] = len(letters)
		letters = append(letters, LibraryBrowseLetter{Letter: initial, Offset: offset, Count: 1})
	}
	return letters
}
