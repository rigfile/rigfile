-- Stage 8: organisations. A namespace several people publish under. A rig owned by an organisation is visible, when private,
-- to the organisation's members (not to whoever happened to create it), so membership is the access control.

CREATE TABLE orgs (
    id          bigserial   PRIMARY KEY,
    login       text        NOT NULL UNIQUE CHECK (login ~ '^[a-z0-9][a-z0-9-]{0,38}$'),
    name        text        NOT NULL DEFAULT '' CHECK (length(name) <= 80),
    created_by  bigint      NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz
);

CREATE TABLE org_members (
    org_id   bigint      NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id  bigint      NOT NULL REFERENCES users(id),
    role     text        NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX org_members_user ON org_members (user_id);

ALTER TABLE rigs ADD COLUMN org_id bigint REFERENCES orgs(id);
CREATE INDEX rigs_org ON rigs (org_id);

-- Users and organisations share one namespace (the `owner` part of owner/name). Two accounts with the same name, one a person
-- and one an organisation, would let either publish as the other, so the database refuses it. The advisory lock makes two
-- simultaneous claims of one name take turns.
CREATE FUNCTION namespace_free() RETURNS trigger AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('rigfile-namespace:' || NEW.login));
    IF TG_TABLE_NAME = 'users' AND EXISTS (SELECT 1 FROM orgs WHERE login = NEW.login) THEN
        RAISE EXCEPTION 'the name % belongs to an organisation', NEW.login USING ERRCODE = '23505';
    END IF;
    IF TG_TABLE_NAME = 'orgs' AND EXISTS (SELECT 1 FROM users WHERE login = NEW.login) THEN
        RAISE EXCEPTION 'the name % belongs to a user', NEW.login USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_namespace BEFORE INSERT OR UPDATE OF login ON users FOR EACH ROW EXECUTE FUNCTION namespace_free();
CREATE TRIGGER orgs_namespace  BEFORE INSERT OR UPDATE OF login ON orgs  FOR EACH ROW EXECUTE FUNCTION namespace_free();
