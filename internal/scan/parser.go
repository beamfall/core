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

// PartMarkerTitler is an optional extension of FilenameParser for parsers that
// group several files into one Media's ordered Parts. It supplies the
// human-facing navigation Marker title for one Part without adding domain
// vocabulary to Core. Parsers that do not implement it fall back to the file
// title.
type PartMarkerTitler interface {
	PartMarkerTitle(path string, probe ParseProbe) string
}
