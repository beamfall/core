INSERT OR IGNORE INTO media_external_id(media_id, scheme, value)
SELECT media_id, 'feed-guid', value
FROM media_external_id
WHERE scheme = 'podcast-guid';

DELETE FROM media_external_id
WHERE scheme = 'podcast-guid';
