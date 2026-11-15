-- Admin API token access tiers (CORE-2095). 'cli' is the CLI allowlist every
-- token had before tiers; 'diagnostics' adds every admin read; 'agent' adds
-- admin mutations except credential minting and owner transfer. Elevated
-- tiers are minted only offline by `beamfall token create --access`.
ALTER TABLE api_token ADD COLUMN access TEXT NOT NULL DEFAULT 'cli'
  CHECK (access IN ('cli', 'diagnostics', 'agent'));
