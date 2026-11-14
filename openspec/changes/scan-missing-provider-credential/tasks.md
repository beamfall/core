## 1. Server

- [x] 1.1 `scrape.CredentialMissingError` for an absent AuthRef or login secret field, path-free
- [x] 1.2 `scan`: fold a missing credential into one WARN per scan, kept in `Result` and the job checkpoint
- [x] 1.3 Completed scan job message names the missing credential
- [x] 1.4 Tests: typed path-free error, one WARN for many files, other errors stay per file, checkpoint round trip
