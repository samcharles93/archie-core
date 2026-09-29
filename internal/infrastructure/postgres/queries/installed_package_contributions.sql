-- name: LockInstalledPackageContributions :exec
SELECT pg_advisory_xact_lock(hashtextextended('installed-package-contributions:' || $1::text || ':' || $2::text, 0));

-- name: InsertInstalledPackageContribution :exec
INSERT INTO installed_package_contributions (org_id, package_name, family, entry_id)
VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING;

-- name: ListInstalledPackageContributions :many
SELECT org_id, package_name, family, entry_id FROM installed_package_contributions
WHERE org_id = $1 AND package_name = $2;

-- name: DeleteInstalledPackageContributions :exec
DELETE FROM installed_package_contributions
WHERE org_id = $1 AND package_name = $2 AND family = $3;