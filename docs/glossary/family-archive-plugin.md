# Family Archive Plugin vocabulary


**Family Archive Plugin** *(ADR-0059, 2026-06-17)*:
The first-party **Domain Plugin** for a household's own home videos and personal archive
Media. Launch-committed as the clean public proof of the plugin thesis: a new domain made of
declared Kinds, Layouts, Sources, local metadata, sidecars, and curation rather than
hardcoded Movie/Show schema. Network-silent by default — it uses file-intrinsic metadata,
folder names, sidecars, and manual curation, not external catalog scrapers. Identification
defaults to the **Unmatched queue** because personal archive items rarely have public catalog
identity.
_Avoid_: treating home videos as Film & TV content, folding this into **Photos**, remote
catalog matching for personal memories.

**Event / Trip / Person / Place** *(Family Archive Kinds, 2026-06-17)*:
The Family Archive organizing model. **Event** and **Trip** are container Kinds for grouped
memories; **Person** is a person-archetype Kind; **Place** is a category Kind for location
facets and may become hierarchical if real libraries need region/city/venue rollups. The home
video or archive item itself is **Media**, never an Event or Trip.
_Avoid_: making home videos container Entities, separate schema types for family memories,
using **Photo** albums as the archive model.
