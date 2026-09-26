-- name: LockInstalledPackages :exec
SELECT pg_advisory_xact_lock(hashtextextended('installed-packages:' || $1::text, 0));

-- name: InsertInstalledPackage :exec
INSERT INTO installed_packages (org_id, name, reference, digest, descriptor, layer, update_policy)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: InsertInstalledRequirement :exec
INSERT INTO installed_package_requirements (org_id, package_name, required_name, required_digest)
VALUES ($1, $2, $3, $4);

-- name: GetInstalledPackage :one
SELECT org_id, name, reference, digest, descriptor, layer, update_policy
FROM installed_packages WHERE org_id = $1 AND name = $2;

-- name: ListInstalledPackages :many
SELECT org_id, name, reference, digest, descriptor, layer, update_policy
FROM installed_packages WHERE org_id = $1 ORDER BY name;

-- name: DeleteInstalledPackage :execrows
DELETE FROM installed_packages WHERE org_id = $1 AND name = $2;

-- name: GetInstalledRequirement :one
SELECT required_digest FROM installed_package_requirements
WHERE org_id = $1 AND package_name = $2 AND required_name = $3;
