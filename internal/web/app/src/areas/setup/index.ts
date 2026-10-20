// SETUP area (WEB-1, ADR-0007 §2): the actionable first-run setup wizard. A
// 6-step, token-gated flow reachable at /setup that ORCHESTRATES existing
// server mutations (POST /api/setup, createLibrary, setLibraryRoots,
// scanLibrary, setPluginActive) — it never duplicates them. The orchestration
// is decoupled from rendering (orchestrate.ts) so the request flow is
// unit-tested directly.
export { SetupWizard } from "./SetupWizard";
