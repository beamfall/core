## 1. Server

- [x] 1.1 Store read resolving library, year and type for search rows across catalog planes
- [x] 1.2 `searchResult` rows carry the optional fields; schema golden and API docs regenerated
- [x] 1.3 Tests: fields present for a visible Library, absent for a hidden one

## 2. Web

- [x] 2.1 Header search field with shortcut, debounce, cancellation, grouped rows and Play control
- [x] 2.2 Search page: query in the URL, search as you type, no-result state with Clear search
- [x] 2.3 Strings in every locale; unit tests for the header search and the search page

## 3. Verify and close

- [x] 3.1 Verify in the running web app on the demo seed, including the live-region announcement
- [x] 3.2 Run `make gate`
- [ ] 3.3 Run `openspec archive web-header-search` after landing, before completing CORE-1191
