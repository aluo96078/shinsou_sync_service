-- +goose Up
-- Sharing works between accounts on the same server: follows, blocks, share inbox.

-- Who may send this account a share.
alter table users add column share_policy text not null default 'everyone'
    check (share_policy in ('everyone', 'following', 'nobody'));

-- Prefix search on user names.
create index users_username_prefix_idx on users (lower(username) text_pattern_ops);

create table follows (
    follower_id  uuid not null references users(id) on delete cascade,
    followee_id  uuid not null references users(id) on delete cascade,
    created_at   timestamptz not null default now(),
    primary key (follower_id, followee_id),
    check (follower_id <> followee_id)
);
create index follows_followee_idx on follows (followee_id, created_at desc);

create table blocks (
    blocker_id  uuid not null references users(id) on delete cascade,
    blocked_id  uuid not null references users(id) on delete cascade,
    created_at  timestamptz not null default now(),
    primary key (blocker_id, blocked_id),
    check (blocker_id <> blocked_id)
);
create index blocks_blocked_idx on blocks (blocked_id);

create table shares (
    id            uuid primary key default gen_random_uuid(),
    sender_id     uuid not null references users(id) on delete cascade,
    recipient_id  uuid not null references users(id) on delete cascade,
    work_id       bigint not null references works(id) on delete cascade,
    message       text not null default '' check (char_length(message) <= 500),
    status        text not null default 'pending' check (status in ('pending', 'accepted', 'dismissed')),
    created_at    timestamptz not null default now(),
    responded_at  timestamptz,
    check (sender_id <> recipient_id)
);
-- One open share per sender, recipient and work; sending it again returns the open one.
create unique index shares_pending_key on shares (sender_id, recipient_id, work_id) where status = 'pending';
create index shares_inbox_idx on shares (recipient_id, created_at desc);
create index shares_sent_idx on shares (sender_id, created_at desc);
create index shares_work_idx on shares (work_id);

-- +goose Down
drop table shares;
drop table blocks;
drop table follows;
drop index users_username_prefix_idx;
alter table users drop column share_policy;
