CREATE TABLE media_favorite (
  profile_id TEXT NOT NULL,
  media_id TEXT NOT NULL,
  favorited_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, media_id)
);

CREATE TABLE follow (
  profile_id TEXT NOT NULL,
  subscription_id TEXT NOT NULL,
  followed_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, subscription_id)
);
