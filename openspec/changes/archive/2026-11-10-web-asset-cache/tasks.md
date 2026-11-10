## 1. Server

- [x] 1.1 Send the immutable cache header for hashed `/web/assets/` files
- [x] 1.2 Gzip text assets once per process, honouring `Accept-Encoding` and `Range`
- [x] 1.3 Server tests for headers, encodings and the missing-asset case

## 2. Service worker

- [x] 2.1 Store fetched `/web/assets/` responses in the versioned cache
- [x] 2.2 Test the store and skip cases

## 3. Verify and close

- [x] 3.1 Run `make gate`
- [x] 3.2 Run `openspec archive web-asset-cache` after landing, before completing CORE-0963
