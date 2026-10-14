-- A personal key (keys_personal.go): one a user made for themselves, under
-- the key or account they were acting with (parent), named so they can
-- tell their keys apart. Unlike a sign-in's key it is not ended by signing
-- out or by a new password.
ALTER TABLE api_keys ADD COLUMN personal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD COLUMN name TEXT NOT NULL DEFAULT '';
