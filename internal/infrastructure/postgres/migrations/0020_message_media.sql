-- +goose Up
-- Attachment metadata on stored chat messages (messaging.Message.Media):
-- type, platform attachment id, name, mime, size. The attachment bytes are
-- deliberately not kept -- they are turn-scoped -- so this is a JSON
-- metadata list, not a blob column.
ALTER TABLE messages ADD COLUMN media jsonb NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE messages DROP COLUMN media;