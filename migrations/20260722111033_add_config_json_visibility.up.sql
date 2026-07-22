-- module: core
ALTER TABLE configs
  ADD COLUMN value_format VARCHAR(16) NOT NULL DEFAULT 'string' AFTER encrypted,
  ADD COLUMN visibility VARCHAR(16) NOT NULL DEFAULT 'admin' AFTER value_format;
