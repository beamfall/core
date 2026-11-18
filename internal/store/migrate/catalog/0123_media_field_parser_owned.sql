-- CORE-2554: a rescan refreshes the Media fields the filename parser owns, so a
-- parser fix (a file classified anew) reaches an unchanged file. The
-- provenance row says which writer tier last won a field; parser_owned says
-- whether that write was the parser's own output:
--   0 = not parser-owned (identification, NFO, manual, hydration, local
--       metadata, artwork flags): a rescan never touches it.
--   1 = the parser's output, independent of the media probe: refreshed by
--       every rescan parse.
--   2 = an older scan-tier class, written before this marker: the next
--       rescan re-reads it with a fresh probe, claims it when the parse emits
--       a class and otherwise disowns it, never removing it. Other older
--       scan-tier fields stay 0, since hydration writes at that tier too.
--   3 = the parser's output that depends on the media probe: refreshed only
--       by a parse that saw a real probe.
ALTER TABLE media_field_provenance ADD COLUMN parser_owned INTEGER NOT NULL DEFAULT 0;
UPDATE media_field_provenance SET parser_owned = 2
  WHERE source = 'scan' AND field = 'class';
