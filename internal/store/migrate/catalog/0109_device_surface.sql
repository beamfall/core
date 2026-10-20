ALTER TABLE device
  ADD COLUMN device_surface TEXT NOT NULL DEFAULT 'core-web-ui'
  CHECK (device_surface IN (
    'core-web-ui',
    'macos-manager',
    'ios-viewer',
    'ipad-manager',
    'tvos-viewer',
    'android-viewer'
  ));
