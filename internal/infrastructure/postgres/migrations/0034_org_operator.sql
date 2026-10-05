-- The dashboard acts as a person who owns org-sys. That person is created by
-- a new resumable upgrade phase, so the ledger's phase vocabulary grows by
-- one.

-- +goose Up

ALTER TABLE org_upgrades DROP CONSTRAINT org_upgrades_phase_check;
ALTER TABLE org_upgrades ADD CONSTRAINT org_upgrades_phase_check
	CHECK (phase IN ('state_store', 'edastore', 'operator'));

-- +goose Down

ALTER TABLE org_upgrades DROP CONSTRAINT org_upgrades_phase_check;
ALTER TABLE org_upgrades ADD CONSTRAINT org_upgrades_phase_check
	CHECK (phase IN ('state_store', 'edastore'));
