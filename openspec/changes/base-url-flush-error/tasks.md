## 1. Server

- [ ] 1.1 Implement `FlushError` on the base-URL response writer, keeping `Flush`
- [ ] 1.2 Tests in `internal/server/base_url_response_writer_test.go`

## 2. Verify and close

- [ ] 2.1 Run `make gate`
- [ ] 2.2 Run `openspec archive base-url-flush-error` after landing, before completing CORE-0840
