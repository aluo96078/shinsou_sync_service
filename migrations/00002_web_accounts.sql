-- +goose Up
-- Web front end and account management: mail tokens, invite management, library view index.

-- Single-use tokens sent by mail. Only the sha256 of the token is stored.
create table email_tokens (
    token_hash  bytea primary key,
    user_id     uuid not null references users(id) on delete cascade,
    purpose     text not null check (purpose in ('verify_email', 'reset_password')),
    email       text not null,                      -- address the mail went to; must still match on use
    expires_at  timestamptz not null,
    used_at     timestamptz,
    created_at  timestamptz not null default now()
);
create index email_tokens_user_idx on email_tokens (user_id, purpose, created_at desc);
create index email_tokens_expires_idx on email_tokens (expires_at);

-- Invites get a public id (the code itself is never stored) and an optional note.
alter table invites add column id uuid not null default gen_random_uuid();
alter table invites add column note text;
create unique index invites_id_key on invites (id);

-- Chapter states of one work, for the library view.
create index sync_records_unit_work_idx on sync_records (user_id, work_id) where kind = 'unit';

-- +goose Down
drop index sync_records_unit_work_idx;
drop index invites_id_key;
alter table invites drop column note;
alter table invites drop column id;
drop table email_tokens;
