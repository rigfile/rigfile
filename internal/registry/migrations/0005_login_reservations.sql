-- Owner decision 2026-09-26: a GitHub login that someone vacated by renaming stays theirs when it names published rigs.
-- Otherwise a stranger who takes the freed GitHub name could sign in as `alice` and publish new rigs under `alice/`, a name
-- other people already trust (docs/registry-security.md §1). Only logins that own at least one rig are reserved: that is
-- what the reservation protects, and it stops anyone squatting names just by cycling through GitHub logins.
CREATE TABLE login_reservations (
    login       text        PRIMARY KEY CHECK (login = lower(login)),
    user_id     bigint      NOT NULL REFERENCES users(id),
    reserved_at timestamptz NOT NULL DEFAULT now()
);

-- Renames that happened before this migration: a rig owner string that is no longer anybody's current login.
INSERT INTO login_reservations (login, user_id)
SELECT r.owner, min(r.created_by)
  FROM rigs r
 WHERE r.org_id IS NULL
   AND NOT EXISTS (SELECT 1 FROM users u WHERE u.login = r.owner)
   AND NOT EXISTS (SELECT 1 FROM orgs o WHERE o.login = r.owner)
 GROUP BY r.owner;

-- Same advisory lock key as namespace_free(), so a claim of the name and a rename away from it take turns.
CREATE FUNCTION login_reserved() RETURNS trigger AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('rigfile-namespace:' || NEW.login));
    IF TG_TABLE_NAME = 'orgs' THEN
        IF EXISTS (SELECT 1 FROM login_reservations WHERE login = NEW.login) THEN
            RAISE EXCEPTION 'the name % is reserved for a renamed account', NEW.login USING ERRCODE = '23505', HINT = 'login_reserved';
        END IF;
        RETURN NEW;
    END IF;
    -- Compared by GitHub id, not row id: an upsert fires this trigger as an INSERT (with a fresh row id) before it turns into an UPDATE.
    IF EXISTS (SELECT 1 FROM login_reservations lr JOIN users u ON u.id = lr.user_id WHERE lr.login = NEW.login AND u.github_id <> NEW.github_id) THEN
        RAISE EXCEPTION 'the name % is reserved for a renamed account', NEW.login USING ERRCODE = '23505', HINT = 'login_reserved';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.login <> NEW.login THEN
        DELETE FROM login_reservations WHERE login = NEW.login AND user_id = NEW.id;
        IF EXISTS (SELECT 1 FROM rigs WHERE owner = OLD.login AND created_by = OLD.id AND org_id IS NULL) THEN
            INSERT INTO login_reservations (login, user_id) VALUES (OLD.login, OLD.id) ON CONFLICT (login) DO NOTHING;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_login_reserved BEFORE INSERT OR UPDATE OF login ON users FOR EACH ROW EXECUTE FUNCTION login_reserved();
CREATE TRIGGER orgs_login_reserved  BEFORE INSERT OR UPDATE OF login ON orgs  FOR EACH ROW EXECUTE FUNCTION login_reserved();
