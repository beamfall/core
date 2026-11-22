# Read declared child listings in Fix Match

CORE-3127 implements the read in ADR-0068 amendment H. Managers can see which child levels a
scraper declares and list those rows for a visible item and candidate.

The read uses the scraper transport and its access controls. Search and child listings share
the plugin SDK list-path dialect. The change adds GraphQL and web client types without a UI.
