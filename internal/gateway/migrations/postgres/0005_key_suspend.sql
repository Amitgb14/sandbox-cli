-- A suspended key (keys_suspend.go): revoked now, and restorable until
-- restore_until. NULL, or a time passed, is a revocation for good.
ALTER TABLE api_keys ADD COLUMN restore_until TIMESTAMPTZ;
