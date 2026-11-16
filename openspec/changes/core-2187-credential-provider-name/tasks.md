## 1. Server

- [x] 1.1 `scan.CredentialLabeler` optional capability; the completion message and WARN name credentials through it, never by ref
- [x] 1.2 `app.scanIdentifier.CredentialLabel` resolves plugin display name and `CredentialHelp` label from the registry
- [x] 1.3 Tests: scan status text for a missing provider credential, generic fallback and label dedupe; adapter labels with and without `CredentialHelp` and rejects unknown refs
