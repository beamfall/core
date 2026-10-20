---
name: writing-a-scraper
description: Authoring guide for a Beamfall metadata-scraper bundle — the declarative ScraperSpec shape end-to-end, with the filmtv/TMDB bundle as the worked example.
updated: 2026-06-28
---

# Writing a metadata-scraper bundle

A **scraper** is the one Stash-style thing the plugin contract already lets a third party ship:
a declarative *fetch-and-map* definition that turns a remote API (or a local file) into core
`Media` fields, artwork, and hydrated Entities. No code runs in the scraper — a `ScraperSpec` is
URL templates plus path mappings (ADR-0003 §3). The engine does the fetching; your bundle only
declares *what to ask for* and *where the answers live*.

This guide documents the `ScraperSpec` shape end-to-end and uses the shipping **Film & TV / TMDB**
bundle (`beamfall-plugin-filmtv/bundle.go`) as the worked example. Every field name below is the
exact identifier in the Plugin SDK (`github.com/beamfall/plugin-sdk`, `plugin.go`); cross-check
that file when in doubt — the SDK is the contract, this guide is the walkthrough.

> **Scope reminder (invariant 3).** A scraper adds *data and declarations* against small fixed
> vocabularies — it never adds schema types, query strings, or rendering code. If you find
> yourself wanting a new field the core `Media` doesn't have, declare a plugin Media field on the
> Bundle and route to it through the `media.` namespace (below) — don't reach for a new schema type.

## 1. Where a scraper lives

Scrapers are declared on the plugin `Bundle`:

```go
type Bundle struct {
    // …
    Scrapers []ScraperSpec
    // …
}
```

A bundle declares **one `ScraperSpec` per identification source/subdivision**. The filmtv bundle
declares two — `tmdb-movie` and `tmdb-tv` — because TMDB exposes movies and TV under different
endpoints and the *choice of scraper* is itself the movie-vs-episode classification (see
`ImpliedMediaFields` below).

## 2. The `ScraperSpec` shape

### Identity and transport

| Field | Meaning |
|---|---|
| `ID` | Lowercase-slug id, unique within the bundle (`"tmdb-movie"`). |
| `DisplayName` | Human name shown to operators (`"TMDB (Movies)"`). |
| `Kind` | Transport kind — one of `ScraperKinds`: `"http-json"`, `"http-graphql"`, `"rss"`, `"exif"`. |
| `Endpoint` | Base URL. **`https://` is required** for the network kinds (outside tests). |

`Kind` picks the transport, and the validator (`internal/plugin/plugin.go`) enforces what each kind
may declare:

- **`http-json`** — JSON over HTTPS; `FieldMap`/`ArtworkMap` paths are dotted JSON paths.
- **`http-graphql`** — GraphQL; templates carry the query body, paths are selection paths.
- **`rss`** — feed URL template; **cannot declare a `SearchTemplate`** (feeds are lookup-only).
- **`exif`** — the lone **local-file** kind (`LocalFileScraperKind` reports true). It reads
  embedded EXIF from the image Media's own file: **no `Endpoint`, no templates, no auth, no
  candidate paths, no field maps, no entity maps** — declaring any of those is a load-time error.
  It runs in the scan pass, not the network identification pipeline (ADR-0043 §2.3).

### Authentication

Secrets never live in the spec. `AuthRef` *names* a config/secret-store entry; the operator
supplies the value.

| Field | Meaning |
|---|---|
| `AuthRef` | Names the config/secret-store entry holding the credential (`"tmdb-api-key"`). |
| `AuthStyle` | How the secret is presented — one of `AuthStyleForms`. Empty means `"bearer"`. |
| `Login` | A `*LoginSpec`, required by **and only by** `AuthStyle == "login-bearer"`. |

`AuthStyleForms` (the fixed vocabulary):

| Form | Effect |
|---|---|
| `"bearer"` | `Authorization: Bearer <secret>` (the default). |
| `"header:<name>"` | request header `<name>: <secret>` (X-Api-Key services). |
| `"query:<param>"` | query parameter `?<param>=<secret>` (TMDB v3 `api_key`). |
| `"login-bearer"` | declarative token exchange via `LoginSpec`, then Bearer. |

Rules the validator enforces: a non-empty `AuthStyle` requires an `AuthRef`; `login-bearer` and a
non-nil `Login` require each other (you cannot declare one without the other).

`LoginSpec` declares the token exchange as pure data (the TheTVDB v4 `POST /login` shape): the
engine POSTs `{SecretField: <secret>}` as JSON to `Endpoint+Path` and uses the string at
`TokenPath` in the response as the bearer token.

```go
type LoginSpec struct {
    Path        string // appended to Endpoint; must start with "/"
    SecretField string // JSON body key carrying the secret ("apikey")
    TokenPath   string // JSON path of the issued token ("data.token")
}
```

### Templates: search → candidate → lookup

Identification is a chain (ADR-0003 §4): **search** free text into a candidate list, pick a
candidate, then **lookup** that candidate's id for the full document.

| Field | Meaning |
|---|---|
| `SearchTemplate` | Free-text identification request. `{query}`, `{page}` (1-based), and fingerprint vars `{phash}`/`{oshash}`/`{duration}` substitute. Requires `CandidatesPath`. Not valid on `rss`. |
| `LookupTemplate` | Per-item request: path (`http-json`), GraphQL body (`http-graphql`), or feed URL (`rss`). `{id}` and the fingerprint vars substitute. |
| `MaxPages` | Caps search pages the engine may fetch. Zero means one page. Must not be negative. |
| `CandidatesPath` | Response path of the search results array (`"results"` for TMDB, `"data"` for TVDB). `FieldMap`/`ArtworkMap` apply per array element. |
| `CandidateIDPath` | Element-relative path of a candidate's id — the value fed back into `LookupTemplate`'s `{id}`. **Required when both templates are declared.** |

A scraper must declare **at least one** of `SearchTemplate` / `LookupTemplate`. Candidate paths
without a `SearchTemplate` are an error; search maps without a `SearchTemplate` are an error.
Unknown fingerprint vars substitute as the empty string — placeholders never reach a remote API
literally.

### Mapping: fields, artwork, entities

This is where a response document becomes core data. Paths resolve against the **lookup document**,
and — when no search-scoped map is declared — against **one search candidate element** too.

| Field | Meaning |
|---|---|
| `FieldMap` | core field name → response path. Keys are the bare `BuiltinMediaFields` (`title`, `summary`, `year`) or `"media.<name>"` for a declared plugin Media field. |
| `ArtworkMap` | artwork slot → response path carrying an image URL. |
| `FieldTypes` | optional field name → type (`FieldTypes` vocab). Undeclared fields stay text. |
| `SearchFieldMap` | non-nil **replaces** `FieldMap` for search candidates whose shape diverges from the lookup document; nil falls back to `FieldMap`. Requires a `SearchTemplate`. |
| `SearchArtworkMap` | the artwork twin of `SearchFieldMap`; same slot vocabulary, same fall-back rule. |
| `Transforms` | field name → one of `TransformKinds`. One vocabulary for both templates. |
| `ArtworkBaseURL` | prefixed onto site-relative `ArtworkMap` values (TMDB's `/abc.jpg` → CDN). Absolute URLs pass through. https required. |
| `ArtworkURLTemplates` | slot → HTTPS template with `{value}`, for rendering non-URL artwork IDs into absolute URLs. |
| `EntityMaps` | lookup-document → Entity hydration (see §3). JSON kinds only; requires a `LookupTemplate`. |
| `ImpliedMediaFields` | declared text Media field → constant value, written verbatim on a successful identify. |
| `MinRequestIntervalMS` | minimum ms between two engine requests for this scraper (rate-limit etiquette; MusicBrainz ~1 req/s). Zero = unthrottled; must not be negative. |

**The two-namespace `FieldMap` rule (the typo trap, BF-24).** A `FieldMap`/`SearchFieldMap` key
resolves in exactly two namespaces: a bare `BuiltinMediaFields` key (`title`/`summary`/`year`), or
`"media.<name>"` naming a Media field your Bundle declares. **Any other bare key — or any
`"entity."` key — is a load-time error, not a silent no-op.** Entity routing is `EntityMap`'s job,
not `FieldMap`'s; an `"entity."` key error points you there deliberately.

`TransformKinds` (the fixed value-transform vocabulary — shape-named post-processing, never a DSL):

| Kind | Effect |
|---|---|
| `"year-from-date"` | `YYYY-MM-DD…` or RFC-822 date → `"YYYY"`. |
| `"date-from-rfc822"` | RFC-822 date (RSS `pubDate`) → `"YYYY-MM-DD"`. |
| `"seconds-from-clock"` | `"HH:MM:SS"`/`"MM:SS"`/plain seconds → seconds. |

A transform must name a field that `FieldMap` or `SearchFieldMap` actually maps, and a value the
transform cannot parse is omitted — the same "omit, never error" posture as an unresolved path.

## 3. `EntityMap`: hydrating Entities from a document

An `EntityMap` turns values in the lookup document into linked Entities (TMDB's `genres` and
`production_companies` become Genre and Studio Entities linked to the Media). Applying an
identification creates one Entity of the declared `Kind` per value at `Path` and links each to the
Media through the declared `Relation`.

| Field | Meaning |
|---|---|
| `Kind` | a declared Entity Kind the hydrated Entities instantiate. |
| `Relation` | a Media-targeted relation declared on `Kind`; one relation row written per Entity. |
| `Path` | lookup-document path of the value(s): a scalar yields one Entity, an array yields one per element. |
| `NamePath` | element-relative name path when `Path` yields a list of objects (`"name"`). Empty = the resolved value is itself the name. |
| `ArtworkPath` | element-relative image URL for the Entity (composes with `ArtworkBaseURL`). Empty = no entity artwork. |
| `FieldMap` | declared field name on `Kind` → element-relative path; parses against the FieldSpec type. |
| `ParentNamePath` / `ParentRelation` | same-Kind parent link for a Hierarchical Kind; `ParentRelation` is required when `ParentNamePath` is set. |
| `OrdinalField` | names a `FieldMap` key whose numeric value supplies the relation's sort position (must name a declared `FieldMap` key). |
| `LookupTemplate` / `LookupIDPath` | optionally fetch a referenced JSON document per element (`LookupIDPath` resolves the per-element `{id}`) before resolving the paths above. |
| `ArtworkURLTemplate` | renders this EntityMap's `ArtworkPath` scalar into an absolute HTTPS URL. |

Every `EntityMap` path follows the `FieldMap` posture: an unresolved or unparseable value is
**omitted, never an error**.

## 4. Worked example: the TMDB movie scraper

From `beamfall-plugin-filmtv/bundle.go`, the `tmdb-movie` spec — verbatim, the canonical reference:

```go
{
    ID:          "tmdb-movie",
    DisplayName: "TMDB (Movies)",
    Kind:        "http-json",
    Endpoint:    "https://api.themoviedb.org/3",
    AuthRef:     "tmdb-api-key",
    AuthStyle:   "query:api_key",
    // GET /search/movie — {"results": [{id, title, overview,
    // release_date, poster_path, backdrop_path}, …]}.
    SearchTemplate:  "/search/movie?query={query}",
    CandidatesPath:  "results",
    CandidateIDPath: "id",
    // GET /movie/{id} — same keys as a search element.
    LookupTemplate: "/movie/{id}",
    FieldMap: map[string]string{
        "title":   "title",
        "summary": "overview",
        "year":    "release_date", // → year transform below
    },
    Transforms: map[string]string{"year": "year-from-date"},
    ArtworkBaseURL: "https://image.tmdb.org/t/p/original",
    ArtworkMap: map[string]string{
        "poster":   "poster_path",
        "backdrop": "backdrop_path",
    },
    // Choosing the movie scraper IS the movie-vs-episode classification
    // (ADR-0028 §1); the "media." prefix is the BF-24 namespacing contract.
    ImpliedMediaFields: map[string]string{"media.class": "movie"},
    // /movie/{id} carries genres: [{id, name}] and
    // production_companies: [{id, name, …}] — hydrate each as an Entity.
    EntityMaps: []sdk.EntityMap{
        {Kind: "genre", Relation: "genre-of", Path: "genres", NamePath: "name"},
        {Kind: "studio", Relation: "produced", Path: "production_companies", NamePath: "name"},
    },
}
```

Reading it against §2:

- **No `SearchFieldMap`** — TMDB's search-result element and its `/movie/{id}` detail document
  share keys (`title`, `overview`, `release_date`, …), so one `FieldMap` serves both templates.
  Declare `SearchFieldMap`/`SearchArtworkMap` only when the two shapes diverge (TVDB's top-level
  search elements vs its `data.`-enveloped details, StashDB's element-relative `searchScene` vs its
  `findScene.`-rooted lookup).
- **`AuthStyle: "query:api_key"`** matches TMDB v3, which takes the key as `?api_key=`.
- **`ImpliedMediaFields: {"media.class": "movie"}`** is the keystone: the *choice* of the movie
  scraper carries the classification, so the constant is written verbatim — no document path needed.
  `media.class` names a text Media field the filmtv Bundle declares (the `media.` namespace, BF-24).
- **Two `EntityMaps`** hydrate genres and studios with name-only mappings (no `FieldMap`, no
  artwork) and link each through the relation declared on its Entity Kind.

## 5. The golden-test harness

Before a bundle ships, pin it against the SDK conformance harness
(`github.com/beamfall/plugin-sdk/sdktest`). These are the goldens that red CI before a regression
ships (XCONTRACT-4a / ADR-0122 Tier 1). The filmtv bundle's `conformance_test.go` is the template:

```go
func TestBundleConformance(t *testing.T) {
    sdktest.RunBundleConformance(t, filmtv.Bundle())
}
```

`RunBundleConformance` asserts the contract every bundle shares: a lowercase-slug `ID`, a semver
`Version`, the `LocaleString` `"en"` anchor on every display string, and `MediaClasses` drawn only
from the fixed `{video, audio, image}` vocabulary. The full scraper validation (https endpoints,
template/candidate consistency, the `FieldMap` two-namespace rule, transform/field-type vocab) runs
at **load time** in `internal/plugin/plugin.go` — your scraper is rejected on activation if any rule
is violated, so a clean activation is itself a conformance signal.

If your bundle also ships compiled hooks (ADR-0044), pin those too — `sdktest.RunConformance`
golden-tests the filename parser against `ParseVector`s, and `sdktest.RunScoreConformance`
golden-tests the match scorer against `ScoreVector`s. The filmtv `conformance_test.go` shows both
with real vectors.

## 6. Checklist

- [ ] One `ScraperSpec` per source/subdivision; `ID` a unique lowercase slug.
- [ ] `Kind` from `ScraperKinds`; `Endpoint` is `https://` (or omitted for `exif`).
- [ ] Secret named by `AuthRef`, never inlined; `AuthStyle` from `AuthStyleForms`.
- [ ] `login-bearer` ⇔ a `*LoginSpec` (one requires the other).
- [ ] At least one of `SearchTemplate` / `LookupTemplate`; `CandidateIDPath` set when both exist.
- [ ] `FieldMap`/`SearchFieldMap` keys are bare `BuiltinMediaFields` or `"media.<name>"` — never a
      typo, never `"entity."`.
- [ ] `Transforms` name mapped fields only; values from `TransformKinds`.
- [ ] `EntityMaps` only with a `LookupTemplate`; parent links carry `ParentRelation`.
- [ ] Bundle pinned with `sdktest.RunBundleConformance` (plus parser/scorer goldens if you ship
      hooks); the bundle activates cleanly under the load-time validator.

See ADR-0003 (declarative plugin infrastructure) and ADR-0028 (the Film & TV plugin) for the
binding decisions behind this shape.
