-- +goose Up
-- Installed packages become a synced record kind (docs/spec/records.md, kind "package").
-- The kind check is the inline column check of 00001_init.sql, which PostgreSQL names
-- sync_records_kind_check.
alter table sync_records drop constraint sync_records_kind_check;
alter table sync_records add constraint sync_records_kind_check check (kind in
    ('library', 'unit', 'progress', 'history', 'category', 'repository', 'setting', 'package'));

-- +goose Down
-- The old check does not allow package records, so they are deleted first. They only tell other
-- devices what to pre-install; no device uninstalls anything because of this, and a device can
-- push its packages again once the server supports the kind.
delete from sync_records where kind = 'package';
alter table sync_records drop constraint sync_records_kind_check;
alter table sync_records add constraint sync_records_kind_check check (kind in
    ('library', 'unit', 'progress', 'history', 'category', 'repository', 'setting'));
