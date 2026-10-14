-- A personal key acts in one organisation (keys_personal.go): the one it
-- was made in. Keys made before this act in their user's first one.
ALTER TABLE api_keys ADD COLUMN org_id TEXT REFERENCES orgs(id);
UPDATE api_keys SET org_id = (SELECT u.org_id FROM users u WHERE u.id = api_keys.user_id) WHERE personal;

-- An account's first organisation is theirs to share (ownFirstOrg): a
-- tenant that is the home of one user, a person who signs in, becomes a
-- team organisation they own, as one they made would be. A tenant several
-- users share, the default one, and one holding only plain keys stay as
-- they are.
UPDATE orgs SET kind = 'team', display_name = key,
	created_by = (SELECT u.id FROM users u WHERE u.org_id = orgs.id)
	WHERE kind = 'personal' AND key <> '' AND key = lower(key) AND (SELECT COUNT(*) FROM users u WHERE u.org_id = orgs.id) = 1
	AND EXISTS (SELECT 1 FROM users u WHERE u.org_id = orgs.id AND u.email IS NOT NULL);
INSERT INTO org_members (org_id, user_id, role, added)
	SELECT o.id, o.created_by, 'owner', o.created FROM orgs o JOIN users u ON u.id = o.created_by
	WHERE o.kind = 'team' AND u.org_id = o.id
	AND NOT EXISTS (SELECT 1 FROM org_members m WHERE m.org_id = o.id AND m.user_id = o.created_by);
