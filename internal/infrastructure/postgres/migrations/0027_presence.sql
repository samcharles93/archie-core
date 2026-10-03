-- One row per running service instance, re-stamped on the restamp interval.

-- +goose Up

CREATE TABLE presence (
	service      text NOT NULL,
	instance_id  text NOT NULL,
	version      text NOT NULL,
	install_type text NOT NULL,
	started_at   timestamptz NOT NULL,
	reported_at  timestamptz NOT NULL,
	ready        boolean NOT NULL,
	detail       text NOT NULL DEFAULT '',
	PRIMARY KEY (service, instance_id)
);

-- +goose Down

DROP TABLE presence;
