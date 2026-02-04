-- Allow long names and URLs (M3U can have very long channel names and stream URLs)
ALTER TABLE sources ALTER COLUMN name TYPE TEXT;
ALTER TABLE groups ALTER COLUMN name TYPE TEXT;
ALTER TABLE channels ALTER COLUMN name TYPE TEXT;
