# TheTVDB v4 response fixtures (CORE-1127, CORE-1128)

Recorded offline from the response schemas in TheTVDB's v4 swagger
(`docs/swagger.yml` v4.8.0, sha256
`ff4a162b2b9192f83abdda672fa64c4f88fe5c1413c6c9eccc7b46a752c633f3`), trimmed
to the fields the scraper maps. No live call was made and no real key was
used. Every series name, overview and id is invented.

| File | Endpoint |
|---|---|
| `login.json` | `POST /login` |
| `search-all.json` | `GET /search?query=Harbor Lights&type=series` |
| `search-2021.json` | `GET /search?query=Harbor Lights&type=series&year=2021` |
| `series-extended.json` | `GET /series/{id}/extended?meta=translations` |
| `remoteid.json` | `GET /search/remoteid/{id}` (a `SeriesBaseRecord`: translations are language codes only, no overview) |
| `remoteid-none.json` | `GET /search/remoteid/{id}` for an id TheTVDB does not know |
| `artwork-types.json` | `GET /artwork/types` |
