-- +goose Up
-- P0 draft schema for accounts, devices and sync. Sharing and reviews arrive in later migrations.
-- Times are timestamptz in the database and Unix milliseconds on the wire.

create table instance_settings (
    key        text primary key,
    value      jsonb not null,
    updated_at timestamptz not null default now()
);
-- Expected keys: name, description, registration, email_verification, terms_url, privacy_url,
-- contact, features, jwt_signing_key (generated on first start), setup_code_hash.

create table users (
    id               uuid primary key,
    username         text not null,
    email            text,
    email_verified   boolean not null default false,
    password_hash    text not null,                 -- argon2id PHC string
    role             text not null default 'user' check (role in ('user', 'moderator', 'admin')),
    status           text not null default 'active' check (status in ('active', 'suspended')),
    sync_seq         bigint not null default 0,     -- last seq handed out; row lock serialises pushes
    sync_floor_seq   bigint not null default 0,     -- since < floor answers 410
    record_quota     integer,                       -- null = instance default
    created_at       timestamptz not null default now(),
    password_changed_at timestamptz not null default now()
);
create unique index users_username_key on users (lower(username));
create unique index users_email_key on users (lower(email)) where email is not null;

create table invites (
    code_hash   bytea primary key,
    created_by  uuid references users(id) on delete set null,
    max_uses    integer not null default 1,
    uses        integer not null default 0,
    expires_at  timestamptz,
    created_at  timestamptz not null default now()
);

create table devices (
    id              uuid primary key,
    user_id         uuid not null references users(id) on delete cascade,
    sync_device_id  text not null check (sync_device_id ~ '^[A-Za-z0-9_-]{1,64}$'),
    name            text not null,
    platform        text not null,
    app_version     text,
    created_at      timestamptz not null default now(),
    last_seen_at    timestamptz not null default now(),
    revoked_at      timestamptz
);
create unique index devices_sync_device_id_key on devices (user_id, sync_device_id);
create index devices_user_idx on devices (user_id) where revoked_at is null;

-- One refresh-token family per device. Only hashes are stored.
create table refresh_tokens (
    token_hash   bytea primary key,                 -- sha256 of the opaque token
    device_id    uuid not null references devices(id) on delete cascade,
    issued_at    timestamptz not null default now(),
    expires_at   timestamptz not null,
    rotated_at   timestamptz                         -- set when exchanged; reuse revokes the device
);
create index refresh_tokens_device_idx on refresh_tokens (device_id);

-- Shared work identities (docs/spec/work-key.md). One row per work across all users.
create table works (
    id              bigserial primary key,
    hash            text not null unique check (hash ~ '^[0-9a-f]{64}$'),
    key_version     smallint not null,
    package_id      text not null,
    source_id       text not null,
    remote_id       text not null,
    title           text not null,
    author          text,
    cover_url       text,
    content_kind    text not null check (content_kind in ('manga', 'novel')),
    language        text,
    source_name     text,
    repository_url  text,
    updated_by      uuid references users(id) on delete set null,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now(),
    unique (key_version, package_id, source_id, remote_id)
);

-- Per-user state-based LWW records (docs/spec/sync-semantics.md).
create table sync_records (
    user_id         uuid not null references users(id) on delete cascade,
    kind            text not null check (kind in
                        ('library', 'unit', 'progress', 'history', 'category', 'repository', 'setting')),
    key             text not null,
    work_id         bigint references works(id),
    unit_remote_id  text,
    fields          jsonb not null default '{}',
    clocks          jsonb not null default '{}',
    deleted         jsonb,                          -- {"value": bool, "clock": hlc}
    cleared         text,                           -- hlc
    is_deleted      boolean generated always as (coalesce((deleted ->> 'value')::boolean, false)) stored,
    seq             bigint not null,
    updated_at      timestamptz not null default now(),
    primary key (user_id, kind, key)
);
create unique index sync_records_seq_key on sync_records (user_id, seq);
create index sync_records_tombstones_idx on sync_records (updated_at) where is_deleted;
create index sync_records_work_idx on sync_records (work_id) where work_id is not null;

-- Idempotent push replay for 24 hours.
create table sync_batches (
    user_id     uuid not null references users(id) on delete cascade,
    batch_id    uuid not null,
    response    jsonb not null,
    created_at  timestamptz not null default now(),
    primary key (user_id, batch_id)
);
create index sync_batches_created_idx on sync_batches (created_at);

-- Moderation and admin actions, kept from P1 so later phases append to one log.
create table audit_log (
    id          bigserial primary key,
    actor_id    uuid references users(id) on delete set null,
    action      text not null,
    target_type text,
    target_id   text,
    detail      jsonb,
    created_at  timestamptz not null default now()
);

-- +goose Down
drop table audit_log;
drop table sync_batches;
drop table sync_records;
drop table works;
drop table refresh_tokens;
drop table devices;
drop table invites;
drop table users;
drop table instance_settings;
