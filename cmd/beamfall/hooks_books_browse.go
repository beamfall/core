package main

import (
	"github.com/beamfall/core/internal/app"
	books "github.com/beamfall/plugin-audiobook"
)

// bookAuthorBrowseRule lists a Person in the books plugin's browse (the home
// Authors grid) when some credit names them author, or carries no role at all
// (a scraper's credit, which names the author): a narrator-only Person stays
// reachable from their books but is not an author (CORE-3014).
func bookAuthorBrowseRule() app.EntityBrowseRule {
	return app.EntityBrowseRule{PluginID: books.PluginID, Kind: books.KindPerson, Sources: []app.EntityBrowseSource{
		{Relation: books.RelationCreditedOn, Attribute: books.AttributeRole, Value: books.RoleAuthor},
		{Relation: books.RelationCreditedOn, Attribute: books.AttributeRole},
	}}
}
