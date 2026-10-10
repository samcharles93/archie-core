-- name: LockInstalledPackages :exec
SELECT pg_advisory_xact_lock(hashtextextended('installed-packages:' || $1::text, 0));

-- name: InsertInstalledPackage :exec
INSERT INTO installed_packages (org_id, name, reference, digest, descriptor, layer, update_policy)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: InsertInstalledRequirement :exec
INSERT INTO installed_package_requirements (org_id, package_name, required_name, required_digest)
VALUES ($1, $2, $3, $4);

-- name: GetInstalledPackage :one
SELECT org_id, name, reference, digest, descriptor, layer, update_policy, accepted_authority,
    pending_reference, pending_digest, pending_authority, previous_reference, previous_digest, previous_accepted_authority
FROM installed_packages WHERE org_id = $1 AND name = $2;

-- name: ListInstalledPackages :many
SELECT org_id, name, reference, digest, descriptor, layer, update_policy, accepted_authority,
    pending_reference, pending_digest, pending_authority, previous_reference, previous_digest, previous_accepted_authority
FROM installed_packages WHERE org_id = $1 ORDER BY name;

-- name: AcceptInstalledPackageAuthority :execrows
UPDATE installed_packages SET accepted_authority = $3
WHERE org_id = $1 AND name = $2;

-- name: DeleteInstalledPackage :execrows
DELETE FROM installed_packages WHERE org_id = $1 AND name = $2;

-- name: GetInstalledRequirement :one
SELECT required_digest FROM installed_package_requirements
WHERE org_id = $1 AND package_name = $2 AND required_name = $3;

-- name: SetInstalledPackagePending :execrows
UPDATE installed_packages SET pending_reference = $3, pending_digest = $4, pending_authority = $5
WHERE org_id = $1 AND name = $2;

-- The right-hand sides read the row as it was, so the replaced pin becomes
-- the previous one in the same statement.
-- name: ReplaceInstalledPackage :execrows
UPDATE installed_packages SET
    previous_reference = reference, previous_digest = digest,
    previous_accepted_authority = accepted_authority,
    reference = $3, digest = $4, descriptor = $5, layer = $6, accepted_authority = $7,
    pending_reference = NULL, pending_digest = NULL, pending_authority = NULL
WHERE org_id = $1 AND name = $2;

-- name: DeleteInstalledRequirements :exec
DELETE FROM installed_package_requirements WHERE org_id = $1 AND package_name = $2;

-- name: CountInstalledDependents :one
SELECT count(*) FROM installed_package_requirements WHERE org_id = $1 AND required_name = $2;
