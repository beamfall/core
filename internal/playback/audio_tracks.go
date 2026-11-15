package playback

import "github.com/beamfall/core/internal/store"

// audioSelection is the audio track decision for one negotiated file: every
// audio stream as an AudioTrack (the chosen one marked Selected), and the
// file's default and chosen streams as absolute stream indexes, -1 when the
// file has no audio.
type audioSelection struct {
	tracks      []AudioTrack
	fileDefault int
	chosen      int
}

// resolveAudioTrack chooses the audio track the delivered streams play: a
// selected index that names one of the file's audio streams, else the stream
// the Profile's audio language preferences pick (see preferredAudioStream),
// else the file's default stream: the first with the default disposition, or
// the first audio stream when none has it.
func resolveAudioTrack(streams []store.MediaFileStream, selected *int, languages []string) audioSelection {
	sel := audioSelection{fileDefault: -1, chosen: -1}
	var audio []store.MediaFileStream
	for _, stream := range streams {
		if stream.Type != "audio" {
			continue
		}
		audio = append(audio, stream)
		if stream.Default && sel.fileDefault < 0 {
			sel.fileDefault = stream.Index
		}
	}
	if len(audio) == 0 {
		return sel
	}
	if sel.fileDefault < 0 {
		sel.fileDefault = audio[0].Index
	}
	sel.chosen = sel.fileDefault
	if selected != nil && hasAudioIndex(audio, *selected) {
		sel.chosen = *selected
	} else if preferred := preferredAudioStream(audio, sel.fileDefault, languages); preferred >= 0 {
		sel.chosen = preferred
	}
	sel.tracks = make([]AudioTrack, 0, len(audio))
	for _, stream := range audio {
		sel.tracks = append(sel.tracks, AudioTrack{
			StreamIndex:    stream.Index,
			Codec:          stream.Codec,
			Language:       stream.Language,
			Channels:       stream.Channels,
			Default:        stream.Index == sel.fileDefault,
			Commentary:     stream.Commentary,
			VisualImpaired: stream.VisualImpaired,
			Selected:       stream.Index == sel.chosen,
		})
	}
	return sel
}

// overridesDefault reports whether the chosen track is not the one a player
// of the untouched file would play.
func (sel audioSelection) overridesDefault() bool {
	return sel.chosen != sel.fileDefault
}

// chosenStreams is streams with every audio stream but the chosen one left
// out: the view a decision about the delivered audio (passthrough, the
// transcode target) must judge, since a server stream maps only the chosen
// track. A file with no audio is returned as is.
func (sel audioSelection) chosenStreams(streams []store.MediaFileStream) []store.MediaFileStream {
	if sel.chosen < 0 {
		return streams
	}
	out := make([]store.MediaFileStream, 0, len(streams))
	for _, stream := range streams {
		if stream.Type == "audio" && stream.Index != sel.chosen {
			continue
		}
		out = append(out, stream)
	}
	return out
}

// language is the chosen track's language tag, the audio language subtitle
// pre-selection resolves against. Empty when the file has no audio or the
// probe recorded none.
func (sel audioSelection) language() string {
	for _, track := range sel.tracks {
		if track.Selected {
			return track.Language
		}
	}
	return ""
}

// preferredAudioStream returns the stream index of the audio stream in the
// most preferred language that has one, or -1 when no stream matches any
// preferred language. Within a language a main track beats commentary and
// audio-description tracks, and the file default beats the other main tracks.
func preferredAudioStream(audio []store.MediaFileStream, fileDefault int, languages []string) int {
	for _, preferred := range languages {
		best, bestRank := -1, 3
		for _, stream := range audio {
			if !langEq(stream.Language, preferred) {
				continue
			}
			rank := 1
			switch {
			case stream.Commentary || stream.VisualImpaired:
				rank = 2
			case stream.Index == fileDefault:
				rank = 0
			}
			if rank < bestRank {
				best, bestRank = stream.Index, rank
			}
		}
		if best >= 0 {
			return best
		}
	}
	return -1
}

func hasAudioIndex(audio []store.MediaFileStream, index int) bool {
	for _, stream := range audio {
		if stream.Index == index {
			return true
		}
	}
	return false
}
