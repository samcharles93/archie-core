-- Events the network rules refused at the capture receiver. Not captures:
-- only the source, the sender address and when, kept for a day.

-- +goose Up

CREATE TABLE capture_refusals (
	source     text NOT NULL,
	addr       text NOT NULL,
	refused_at timestamptz NOT NULL
);
CREATE INDEX idx_capture_refusals_source ON capture_refusals (source, refused_at);

-- +goose Down

DROP TABLE capture_refusals;
