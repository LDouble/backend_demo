-- module: core
ALTER TABLE configs
  DROP COLUMN visibility,
  DROP COLUMN value_format;
