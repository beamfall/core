-- CORE-2554: a rescan refreshes the Media fields the filename parser owns, so a
-- parser fix (a file classified anew) reaches an unchanged file. The
-- provenance row says which writer tier last won a field; parser_owned says
-- whether that write was the parser's own output:
--   0 = not parser-owned (identification, NFO, manual, hydration, local
--       metadata, artwork flags): a rescan never touches it.
--   1 = the parser's output, independent of the media probe: refreshed by
--       every rescan parse.
--   2 = an older scan-tier class, written before this marker: the next
--       rescan re-reads it with a fresh probe and replaces it, or removes it
--       when the parse emits no class (only the parser and image local
--       metadata, re-applied every scan, write a scan-tier class).
--   3 = the parser's output that depends on the media probe: refreshed only
--       by a parse that saw a real probe.
--   4 = no value: a probed parse left the field out where a parse without
--       the probe emits it, so only a probed parse may add it back.
--   5 = any other older scan-tier text, number or date value, written before
--       this marker. The parser wrote it unless the Library hydrates metadata
--       or the file is an image (hydration and image local metadata write at
--       the scan tier too), and only a scan knows which. So the next
--       authoritative rescan settles it: with neither, it reconciles it like
--       2 (replaces it, or removes it when the parse no longer emits it);
--       otherwise it keeps the value and marks it 0. Flags (artwork,
--       hydration markers), the provenance of Media columns (no field value)
--       and every manual, operator or AI row stay 0. A value from before
--       provenance was recorded has no row at all and is never touched.
ALTER TABLE media_field_provenance ADD COLUMN parser_owned INTEGER NOT NULL DEFAULT 0;
UPDATE media_field_provenance SET parser_owned = 2
  WHERE source = 'scan' AND field = 'class';
UPDATE media_field_provenance SET parser_owned = 5
  WHERE source = 'scan' AND parser_owned = 0 AND (
    EXISTS (SELECT 1 FROM media_field_text v
      WHERE v.media_id = media_field_provenance.media_id
        AND v.field = media_field_provenance.field AND v.field_type = 'text')
    OR EXISTS (SELECT 1 FROM media_field_real v
      WHERE v.media_id = media_field_provenance.media_id
        AND v.field = media_field_provenance.field)
    OR EXISTS (SELECT 1 FROM media_field_date v
      WHERE v.media_id = media_field_provenance.media_id
        AND v.field = media_field_provenance.field));
