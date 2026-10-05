-- Rename the built-in org from 'default' to 'org-sys'. The default workspace
-- id stays 'default'. Every table that carried the org moves with it, and
-- every column that defaulted to the old id defaults to the new one; the
-- created row is 'System' because that is the name the boot upgrade now uses.

-- +goose Up

-- The row the rest of the migration points at. A fresh install has no
-- 'default' row yet, so this is a no-op and the boot upgrade creates 'org-sys'.
INSERT INTO orgs (id, name)
SELECT 'org-sys', 'System'
WHERE EXISTS (SELECT 1 FROM orgs WHERE id = 'default')
ON CONFLICT (id) DO NOTHING;

-- The installed_package children reference (org_id, name) on their parent, so
-- the parent and children cannot move under an immediate FK; drop the child
-- FKs, move both, then re-add them unchanged.
ALTER TABLE installed_package_requirements
	DROP CONSTRAINT installed_package_requirements_org_id_package_name_fkey,
	DROP CONSTRAINT installed_package_requirements_org_id_required_name_fkey;
ALTER TABLE installed_package_contributions
	DROP CONSTRAINT installed_package_contributions_org_id_package_name_fkey;

UPDATE workspaces SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE memberships SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE org_agents SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE tasks SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE events SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE transitions SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE resources SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE resource_history SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE step_executions SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE access_policies SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE access_policy_versions SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE access_denials SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE bindings SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE mappings SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE captures SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE sources SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE event_types SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE tool_calls SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE installed_packages SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE installed_package_requirements SET org_id = 'org-sys' WHERE org_id = 'default';
UPDATE installed_package_contributions SET org_id = 'org-sys' WHERE org_id = 'default';

ALTER TABLE installed_package_requirements
	ADD CONSTRAINT installed_package_requirements_org_id_package_name_fkey
		FOREIGN KEY (org_id, package_name) REFERENCES installed_packages (org_id, name) ON DELETE CASCADE,
	ADD CONSTRAINT installed_package_requirements_org_id_required_name_fkey
		FOREIGN KEY (org_id, required_name) REFERENCES installed_packages (org_id, name) ON DELETE RESTRICT;
ALTER TABLE installed_package_contributions
	ADD CONSTRAINT installed_package_contributions_org_id_package_name_fkey
		FOREIGN KEY (org_id, package_name) REFERENCES installed_packages (org_id, name) ON DELETE CASCADE;

ALTER TABLE tasks ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE events ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE transitions ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE resources ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE resource_history ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE step_executions ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE bindings ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE mappings ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE captures ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE sources ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE event_types ALTER COLUMN org_id SET DEFAULT 'org-sys';
ALTER TABLE tool_calls ALTER COLUMN org_id SET DEFAULT 'org-sys';

DELETE FROM orgs WHERE id = 'default';

-- +goose Down

INSERT INTO orgs (id, name)
SELECT 'default', 'Default'
WHERE EXISTS (SELECT 1 FROM orgs WHERE id = 'org-sys')
ON CONFLICT (id) DO NOTHING;

ALTER TABLE installed_package_requirements
	DROP CONSTRAINT installed_package_requirements_org_id_package_name_fkey,
	DROP CONSTRAINT installed_package_requirements_org_id_required_name_fkey;
ALTER TABLE installed_package_contributions
	DROP CONSTRAINT installed_package_contributions_org_id_package_name_fkey;

UPDATE workspaces SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE memberships SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE org_agents SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE tasks SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE events SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE transitions SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE resources SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE resource_history SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE step_executions SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE access_policies SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE access_policy_versions SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE access_denials SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE bindings SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE mappings SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE captures SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE sources SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE event_types SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE tool_calls SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE installed_packages SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE installed_package_requirements SET org_id = 'default' WHERE org_id = 'org-sys';
UPDATE installed_package_contributions SET org_id = 'default' WHERE org_id = 'org-sys';

ALTER TABLE installed_package_requirements
	ADD CONSTRAINT installed_package_requirements_org_id_package_name_fkey
		FOREIGN KEY (org_id, package_name) REFERENCES installed_packages (org_id, name) ON DELETE CASCADE,
	ADD CONSTRAINT installed_package_requirements_org_id_required_name_fkey
		FOREIGN KEY (org_id, required_name) REFERENCES installed_packages (org_id, name) ON DELETE RESTRICT;
ALTER TABLE installed_package_contributions
	ADD CONSTRAINT installed_package_contributions_org_id_package_name_fkey
		FOREIGN KEY (org_id, package_name) REFERENCES installed_packages (org_id, name) ON DELETE CASCADE;

ALTER TABLE tasks ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE events ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE transitions ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE resources ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE resource_history ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE step_executions ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE bindings ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE mappings ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE captures ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE sources ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE event_types ALTER COLUMN org_id SET DEFAULT 'default';
ALTER TABLE tool_calls ALTER COLUMN org_id SET DEFAULT 'default';

DELETE FROM orgs WHERE id = 'org-sys';
