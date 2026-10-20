package plugin

// Plugin entity-data lifecycle is intentionally deferred. The former exported
// LifecycleState contract promised persistence, query/index deactivation, and
// uninstall purge without production consumers. Reintroduce a public contract
// only with the store, query/index, and purge implementations in the same change.
