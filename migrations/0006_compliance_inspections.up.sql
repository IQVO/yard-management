-- 0006_compliance_inspections.up.sql (ADR 0015): versioned inspection templates,
-- inspections and the site's required inspections. Additive and backwards
-- compatible: two new tables and one new column that defaults to "no inspection
-- required", so every existing row loads, is judged exactly as before and every
-- statement of the previous release still runs.

-- inspection_templates: the CUSTOM templates a site published through the API
-- (the built-in ones live in code). A version is immutable once published: a
-- new version is a new row, and the primary key refuses a second row for the
-- same (template, version).
CREATE TABLE inspection_templates (
    template_id  TEXT COLLATE "C" NOT NULL,
    version      INTEGER     NOT NULL CHECK (version >= 1),
    title        TEXT        NOT NULL,
    items        JSONB       NOT NULL,
    published_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (template_id, version)
);

-- inspections: the Inspection aggregate. items is the SNAPSHOT of the template
-- version it was opened from plus what was found, so a later template never
-- rewrites it.
CREATE TABLE inspections (
    inspection_id    TEXT COLLATE "C" PRIMARY KEY,
    site_id          TEXT COLLATE "C" NOT NULL,
    trailer_id       TEXT COLLATE "C" NOT NULL,
    visit_id         TEXT COLLATE "C" NOT NULL DEFAULT '',
    template_id      TEXT COLLATE "C" NOT NULL,
    template_version INTEGER     NOT NULL CHECK (template_version >= 1),
    gate             TEXT        NOT NULL CHECK (gate IN ('PRE_DOCK', 'PRE_LOAD', 'PRE_DEPARTURE', 'ON_DEMAND')),
    status           TEXT        NOT NULL CHECK (status IN ('OPEN', 'PASSED', 'FAILED')),
    items            JSONB       NOT NULL,
    performer_ref    TEXT        NOT NULL DEFAULT '',
    condition_grade  TEXT        NOT NULL DEFAULT '' CHECK (condition_grade IN ('', 'CLEAN', 'DIRTY', 'ODOUR', 'DAMAGED')),
    opened_at        TIMESTAMPTZ NOT NULL,
    completed_at     TIMESTAMPTZ,
    version          BIGINT      NOT NULL CHECK (version >= 1),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'OPEN') = (completed_at IS NULL))
);

-- At most one OPEN inspection per trailer, template and gate
-- (ErrInspectionAlreadyOpen).
CREATE UNIQUE INDEX uq_inspections_one_open ON inspections (trailer_id, template_id, gate) WHERE status = 'OPEN';
-- The trailer's inspections, newest first (GET /trailers/{id}/inspections).
CREATE INDEX idx_inspections_trailer ON inspections (trailer_id, opened_at DESC, inspection_id DESC);
-- The completed inspections of a trailer in the order they completed: what the
-- gates of dock, load and departure read.
CREATE INDEX idx_inspections_completed ON inspections (trailer_id, completed_at, inspection_id) WHERE status <> 'OPEN';

-- The inspections a trailer needs before it docks, loads or leaves, a JSON list
-- of {gate, templateId, purposes, trailerKinds}; '[]' (nothing required) is
-- today's behaviour.
ALTER TABLE yard_policies ADD COLUMN required_inspections JSONB NOT NULL DEFAULT '[]'::jsonb;
