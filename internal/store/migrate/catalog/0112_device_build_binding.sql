-- ADR-0063 build provenance: the exact mapping from an enrolled device to the
-- first-party native build it enrolled with. A shared role, distribution class
-- or device surface confers nothing; only a row here lets that device's sessions
-- present that build. Rows are written once at enrollment, after the build has
-- verified against the release catalog, and disappear with the device.
CREATE TABLE device_build_binding (
  device_id TEXT NOT NULL REFERENCES device(id) ON DELETE CASCADE,
  surface TEXT NOT NULL CHECK (surface IN (
    'macos-manager',
    'ios-viewer',
    'ipad-manager',
    'tvos-viewer',
    'android-viewer'
  )),
  build_id TEXT NOT NULL CHECK (length(build_id) BETWEEN 1 AND 256 AND build_id = trim(build_id)),
  bound_at_ms INTEGER NOT NULL,
  PRIMARY KEY (device_id, surface)
) WITHOUT ROWID;
