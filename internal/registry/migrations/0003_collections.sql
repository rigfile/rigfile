-- Stage 8: collections. A collection is a curated list of rigs owned by one user. It holds references only; who may see a
-- rig is still decided by the one visibility predicate, so a collection never reveals a rig.

CREATE TABLE collections (
    id          bigserial   PRIMARY KEY,
    owner_id    bigint      NOT NULL REFERENCES users(id),
    slug        text        NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    title       text        NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
    description text        NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    visibility  text        NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    removed_at  timestamptz
);
CREATE UNIQUE INDEX collections_owner_slug ON collections (owner_id, slug) WHERE removed_at IS NULL;

CREATE TABLE collection_items (
    collection_id bigint      NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    rig_id        bigint      NOT NULL REFERENCES rigs(id),
    note          text        NOT NULL DEFAULT '' CHECK (length(note) <= 200),
    added_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, rig_id)
);
CREATE INDEX collection_items_rig ON collection_items (rig_id);
