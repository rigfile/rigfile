-- Stage 6: trust and supply chain. Held versions (moderation queue), static analysis results, similar-name matches,
-- verified publishers, runtime settings (publishing pause), and signature data.

ALTER TABLE versions DROP CONSTRAINT versions_status_check;
ALTER TABLE versions ADD CONSTRAINT versions_status_check CHECK (status IN ('pending','published','rejected','yanked','removed','held'));

ALTER TABLE versions
    ADD COLUMN scan_analysis      jsonb   NOT NULL DEFAULT '[]',
    ADD COLUMN similar_to         jsonb   NOT NULL DEFAULT '[]',
    ADD COLUMN held_reason        text    NOT NULL DEFAULT '',
    ADD COLUMN bundle             text,
    ADD COLUMN signer_issuer      text    NOT NULL DEFAULT '',
    ADD COLUMN signer_subject     text    NOT NULL DEFAULT '',
    ADD COLUMN signer_is_publisher boolean NOT NULL DEFAULT false,
    ADD COLUMN signature_error    text    NOT NULL DEFAULT '';

ALTER TABLE users
    ADD COLUMN verified_at   timestamptz,
    ADD COLUMN verified_kind text NOT NULL DEFAULT '' CHECK (verified_kind IN ('', 'domain', 'organisation', 'person')),
    ADD COLUMN verified_note text NOT NULL DEFAULT '';

-- A rig that needed an administrator's review before going public (danger-level analysis on its newest version, or a name
-- confusably close to a notable rig) carries the approval here.
ALTER TABLE rigs
    ADD COLUMN public_approved boolean NOT NULL DEFAULT false;

CREATE TABLE settings (
    key        text        PRIMARY KEY,
    value      text        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
