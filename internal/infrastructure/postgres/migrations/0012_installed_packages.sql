-- +goose Up
CREATE TABLE installed_packages (
    org_id text NOT NULL,
    name text NOT NULL,
    reference text NOT NULL,
    digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    descriptor jsonb NOT NULL,
    layer bytea NOT NULL,
    update_policy text NOT NULL DEFAULT 'manual' CHECK (update_policy IN ('manual', 'auto')),
    installed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, name)
);

CREATE TABLE installed_package_requirements (
    org_id text NOT NULL,
    package_name text NOT NULL,
    required_name text NOT NULL,
    required_digest text NOT NULL,
    PRIMARY KEY (org_id, package_name, required_name),
    FOREIGN KEY (org_id, package_name) REFERENCES installed_packages (org_id, name) ON DELETE CASCADE,
    FOREIGN KEY (org_id, required_name) REFERENCES installed_packages (org_id, name) ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE installed_package_requirements;
DROP TABLE installed_packages;
