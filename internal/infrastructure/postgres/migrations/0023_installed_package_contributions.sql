-- +goose Up
-- The entries an org's installed package projected into control-plane
-- resources. Removal reads this ledger to take exactly the package's own
-- contributions back out, leaving every entry the operator or another package
-- owns. Rows cascade with the installed package they describe.
CREATE TABLE installed_package_contributions (
    org_id text NOT NULL,
    package_name text NOT NULL,
    family text NOT NULL,
    entry_id text NOT NULL,
    PRIMARY KEY (org_id, package_name, family, entry_id),
    FOREIGN KEY (org_id, package_name) REFERENCES installed_packages (org_id, name) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE installed_package_contributions;