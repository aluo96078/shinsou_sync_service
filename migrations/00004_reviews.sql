-- +goose Up
-- Reviews of works: one review per account and work, helpful votes, one level of replies, reports.

create table reviews (
    id             uuid primary key default gen_random_uuid(),
    work_id        bigint not null references works(id) on delete cascade,
    user_id        uuid not null references users(id) on delete cascade,
    rating         smallint check (rating between 1 and 5),
    body           text not null default '' check (char_length(body) <= 5000),
    spoiler        boolean not null default false,
    status         text not null default 'visible' check (status in ('visible', 'hidden')),
    hidden_reason  text,
    hidden_at      timestamptz,
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),
    unique (work_id, user_id),
    check (rating is not null or body <> '')
);
create index reviews_work_idx on reviews (work_id, created_at desc);
create index reviews_user_idx on reviews (user_id, created_at desc);

create table review_votes (
    review_id   uuid not null references reviews(id) on delete cascade,
    user_id     uuid not null references users(id) on delete cascade,
    created_at  timestamptz not null default now(),
    primary key (review_id, user_id)
);
create index review_votes_user_idx on review_votes (user_id);

create table review_replies (
    id             uuid primary key default gen_random_uuid(),
    review_id      uuid not null references reviews(id) on delete cascade,
    user_id        uuid not null references users(id) on delete cascade,
    body           text not null check (char_length(body) between 1 and 2000),
    status         text not null default 'visible' check (status in ('visible', 'hidden')),
    hidden_reason  text,
    hidden_at      timestamptz,
    created_at     timestamptz not null default now()
);
create index review_replies_review_idx on review_replies (review_id, created_at);
create index review_replies_user_idx on review_replies (user_id, created_at desc);

-- A report points at a review or at a reply, never both.
create table review_reports (
    id           uuid primary key default gen_random_uuid(),
    reporter_id  uuid not null references users(id) on delete cascade,
    review_id    uuid references reviews(id) on delete cascade,
    reply_id     uuid references review_replies(id) on delete cascade,
    reason       text not null check (reason in ('spam', 'harassment', 'spoiler', 'illegal', 'other')),
    note         text not null default '' check (char_length(note) <= 500),
    status       text not null default 'open' check (status in ('open', 'resolved')),
    resolution   text check (resolution in ('dismissed', 'hidden', 'deleted')),
    resolved_by  uuid references users(id) on delete set null,
    resolved_at  timestamptz,
    created_at   timestamptz not null default now(),
    check ((review_id is null) <> (reply_id is null))
);
-- One open report per reporter and target; reporting again returns the open one.
create unique index review_reports_open_review_key on review_reports (reporter_id, review_id) where status = 'open' and review_id is not null;
create unique index review_reports_open_reply_key on review_reports (reporter_id, reply_id) where status = 'open' and reply_id is not null;
create index review_reports_status_idx on review_reports (status, created_at);
create index review_reports_review_idx on review_reports (review_id) where review_id is not null;
create index review_reports_reply_idx on review_reports (reply_id) where reply_id is not null;

-- +goose Down
drop table review_reports;
drop table review_replies;
drop table review_votes;
drop table reviews;
