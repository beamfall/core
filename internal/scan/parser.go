package scan

import sdk "github.com/beamfall/plugin-sdk"

// Type aliases: scan types now live in the plugin SDK so first-party plugin
// modules can implement the parser seams without importing internal/scan.

type ParsedMedia = sdk.ParsedMedia
type ParsedTextField = sdk.ParsedTextField
type ParsedField = sdk.ParsedField
type ParseProbe = sdk.ParseProbe
type ParseContext = sdk.ParseContext
type DirectoryMediaFileCount = sdk.DirectoryMediaFileCount
type FilenameParser = sdk.FilenameParser
type ContextFilenameParser = sdk.ContextFilenameParser
type MediaFieldParser = sdk.MediaFieldParser
type TypedMediaFieldParser = sdk.TypedMediaFieldParser

const (
	DirectoryMediaFileCountUnknown  = sdk.DirectoryMediaFileCountUnknown
	DirectoryMediaFileCountSingle   = sdk.DirectoryMediaFileCountSingle
	DirectoryMediaFileCountMultiple = sdk.DirectoryMediaFileCountMultiple
)

// MediaTitler is an optional extension of FilenameParser for parsers whose
// parsed title names a container rather than the file (several files share
// it). A non-empty MediaTitle becomes the imported Media's title; the parsed
// title still drives identification. Parsers that do not implement it, or
// return "", keep the parsed title.
type MediaTitler interface {
	MediaTitle(path string, probe ParseProbe) string
}

// IdentifiedMediaTitler is an optional extension of MediaTitler for parsers
// whose per-file title can name what identification found ("S03E01 · Smells
// Like Mean Spirit"). fields are the identification's plugin-declared Media
// field values by bare name, as text. A "" result keeps the MediaTitle rule.
type IdentifiedMediaTitler interface {
	IdentifiedMediaTitle(path string, probe ParseProbe, fields map[string]string) string
}

// PartMarkerTitler is an optional extension of FilenameParser for parsers that
// group several files into one Media's ordered Parts. It supplies the
// human-facing navigation Marker title for one Part without adding domain
// vocabulary to Core. Parsers that do not implement it fall back to the file
// title.
type PartMarkerTitler interface {
	PartMarkerTitle(path string, probe ParseProbe) string
}
