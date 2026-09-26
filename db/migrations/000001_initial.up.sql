CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE user_role AS ENUM ('student', 'admin');
CREATE TYPE content_status AS ENUM ('draft', 'published', 'archived');
CREATE TYPE difficulty_level AS ENUM ('beginner', 'intermediate', 'advanced');
CREATE TYPE attempt_status AS ENUM ('in_progress', 'completed');
CREATE TYPE publication_action AS ENUM ('publish', 'archive');
CREATE TYPE actor_type AS ENUM ('user', 'service_token');
CREATE TYPE export_status AS ENUM ('not_requested', 'pending', 'succeeded', 'warning', 'failed');

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 100),
    password_hash text NOT NULL,
    role user_role NOT NULL DEFAULT 'student',
    is_active boolean NOT NULL DEFAULT true,
    last_login_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_normalized CHECK (email = lower(btrim(email)))
);
CREATE UNIQUE INDEX users_email_unique ON users (lower(email));

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    csrf_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at, id) WHERE revoked_at IS NULL;

CREATE TABLE domains (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    description text NOT NULL DEFAULT '',
    weight smallint NOT NULL CHECK (weight BETWEEN 0 AND 100),
    sort_order smallint NOT NULL UNIQUE CHECK (sort_order > 0),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id uuid NOT NULL REFERENCES domains(id),
    author_id uuid NOT NULL REFERENCES users(id),
    title text NOT NULL,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    summary text NOT NULL DEFAULT '',
    markdown text NOT NULL DEFAULT '',
    status content_status NOT NULL DEFAULT 'draft',
    version integer NOT NULL DEFAULT 0 CHECK (version >= 0),
    reading_time_minutes integer NOT NULL DEFAULT 1 CHECK (reading_time_minutes > 0),
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notes_publication_state CHECK ((status = 'published' AND published_at IS NOT NULL AND version > 0) OR status <> 'published')
);
CREATE INDEX notes_public_idx ON notes(domain_id, published_at DESC) WHERE status = 'published';

CREATE TABLE tags (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$')
);
CREATE TABLE note_tags (
    note_id uuid NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag_id uuid NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (note_id, tag_id)
);
CREATE TABLE note_references (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id uuid NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    title text NOT NULL,
    url text,
    citation text NOT NULL DEFAULT '',
    position integer NOT NULL CHECK (position > 0),
    UNIQUE(note_id, position)
);

CREATE TABLE exams (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id uuid NOT NULL REFERENCES users(id),
    title text NOT NULL,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    description text NOT NULL DEFAULT '',
    difficulty difficulty_level NOT NULL,
    time_limit_minutes integer NOT NULL CHECK (time_limit_minutes > 0),
    pass_percentage numeric(5,2) NOT NULL DEFAULT 70 CHECK (pass_percentage BETWEEN 0 AND 100),
    status content_status NOT NULL DEFAULT 'draft',
    version integer NOT NULL DEFAULT 0 CHECK (version >= 0),
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT exams_publication_state CHECK ((status = 'published' AND published_at IS NOT NULL AND version > 0) OR status <> 'published')
);
CREATE INDEX exams_public_idx ON exams(published_at DESC) WHERE status = 'published';

CREATE TABLE questions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    exam_id uuid NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
    domain_id uuid NOT NULL REFERENCES domains(id),
    prompt text NOT NULL,
    scenario text NOT NULL DEFAULT '',
    explanation text NOT NULL DEFAULT '',
    difficulty difficulty_level NOT NULL,
    position integer NOT NULL CHECK (position > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(exam_id, position)
);
CREATE TABLE question_options (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id uuid NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    option_key text NOT NULL CHECK (option_key ~ '^[A-Z]$'),
    text text NOT NULL,
    is_correct boolean NOT NULL DEFAULT false,
    position integer NOT NULL CHECK (position > 0),
    UNIQUE(question_id, option_key),
    UNIQUE(question_id, position)
);
CREATE TABLE question_references (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id uuid NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    title text NOT NULL,
    url text,
    citation text NOT NULL DEFAULT '',
    position integer NOT NULL CHECK (position > 0),
    UNIQUE(question_id, position)
);

CREATE TABLE exam_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    exam_id uuid NOT NULL REFERENCES exams(id),
    version integer NOT NULL CHECK (version > 0),
    snapshot jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(exam_id, version)
);

CREATE TABLE attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    exam_id uuid NOT NULL REFERENCES exams(id),
    exam_version_id uuid NOT NULL REFERENCES exam_versions(id),
    status attempt_status NOT NULL DEFAULT 'in_progress',
    snapshot jsonb NOT NULL,
    correct_count integer,
    question_count integer NOT NULL CHECK (question_count > 0),
    score_percentage numeric(5,2),
    passed boolean,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT attempts_completion CHECK ((status = 'completed' AND completed_at IS NOT NULL AND correct_count IS NOT NULL AND score_percentage IS NOT NULL AND passed IS NOT NULL) OR status = 'in_progress')
);
CREATE INDEX attempts_user_idx ON attempts(user_id, started_at DESC);

CREATE TABLE attempt_answers (
    attempt_id uuid NOT NULL REFERENCES attempts(id) ON DELETE CASCADE,
    question_id uuid NOT NULL,
    selected_option_id uuid NOT NULL,
    is_correct boolean,
    answered_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(attempt_id, question_id)
);

CREATE TABLE service_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    token_hash bytea NOT NULL UNIQUE,
    scopes text[] NOT NULL DEFAULT '{}',
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

CREATE TABLE publication_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type text NOT NULL CHECK (entity_type IN ('note', 'exam')),
    entity_id uuid NOT NULL,
    version integer NOT NULL CHECK (version >= 0),
    action publication_action NOT NULL,
    actor_type actor_type NOT NULL,
    actor_id uuid NOT NULL,
    reason text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}',
    dry_run boolean NOT NULL DEFAULT false,
    export_status export_status NOT NULL DEFAULT 'not_requested',
    outcome text NOT NULL CHECK (outcome IN ('success', 'warning', 'failure')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX publication_events_entity_idx ON publication_events(entity_type, entity_id, created_at DESC);
CREATE INDEX publication_events_created_idx ON publication_events(created_at DESC);

CREATE TABLE mcp_audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_token_id uuid NOT NULL REFERENCES service_tokens(id),
    tool text NOT NULL,
    target_type text,
    target_id uuid,
    outcome text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mcp_audit_created_idx ON mcp_audit_events(created_at DESC);
