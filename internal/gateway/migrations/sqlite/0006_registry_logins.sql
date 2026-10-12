-- Registry logins (registry_logins.go): a user name and a sealed token per
-- organisation and registry, which the gateway adds to that organisation's
-- creates for a private image.

CREATE TABLE registry_logins (
	org_id   TEXT NOT NULL REFERENCES orgs(id),
	registry TEXT NOT NULL,
	username TEXT NOT NULL,
	sealed   BLOB NOT NULL,
	updated  INTEGER NOT NULL,
	PRIMARY KEY (org_id, registry)
);
