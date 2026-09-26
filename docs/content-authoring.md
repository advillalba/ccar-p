# Content authoring

This document describes how to author study notes and exam questions
for the ccar-p study platform. Authors are operators with the
`admin` role.

## Notes

A note is a Markdown document with structured metadata. The Markdown
is rendered through `internal/content`'s semantic parser, which
produces the HTML for the website.

### Required metadata

- `title` — human-readable title; appears in listings.
- `slug` — URL-safe identifier; lowercase letters, digits, and dashes.
- `summary` — short description; appears in listings.
- `domain` — one of the active domains (see `domains` table).
- `markdown` — the Markdown body. The body must contain at least one
  heading.

### Optional metadata

- `tags` — list of tag names. Tags are deduplicated.
- `references` — bibliographic citations rendered at the end of the
  note. Each reference must include a title and may include a URL
  and citation text.

### Markdown rules

- Headings (`#`, `##`, ...) start sections. The first heading is the
  document title and is not rendered as a section.
- Inline styles supported: emphasis (`*text*`), strong
  (`**text**`), inline code (`` `code` ``), and links
  (`[text](url)`).
- Links must use `http` or `https`. Other schemes are rejected.
- Code blocks use triple-backtick fences with an optional language
  tag. The HTML renderer applies syntax highlighting when the
  language tag is recognized.
- Tables use GitHub-flavored Markdown syntax (pipe-delimited rows).
- Mermaid diagrams are rendered to SVG when the renderer is available.
  When the renderer is unavailable, a textual fallback is rendered
  instead.

### Lifecycle

- `draft` — the note is editable. Only the author can see drafts.
- `published` — the note is read-only and visible to everyone. The
  publication event is recorded in `publication_events`.
- `archived` — the note is hidden from listings but the URL still
  resolves for SEO purposes.

A `published` note is versioned. Updating the note increments the
version.

## Exams

An exam is a structured question bank. Each exam is published as a
versioned snapshot; the snapshot is the artifact served to readers.

### Required metadata

- `title`
- `slug`
- `description`
- `difficulty` — `beginner`, `intermediate`, `advanced`, or `exam_scenarios` (maximum).
- `time_limit_minutes`
- `pass_percentage`
- `questions` — at least one.

### Question structure

Each question has:

- `prompt` — the question text.
- `scenario` — optional preamble shared across questions.
- `explanation` — shown after the exam is submitted.
- `difficulty` — `beginner`, `intermediate`, `advanced`, or `exam_scenarios` (maximum). May differ from the
  exam-wide difficulty.
- `options` — at least two. Exactly one option must be marked as
  correct.
- `references` — citations shown with the explanation.

### Publication

The exam moves through the same lifecycle as notes:
`draft → published → archived`. Publication snapshots the question
bank so future edits do not affect attempts already in progress.

## Audit and review

Every publication event is recorded with the actor, outcome, and
reason. Administrators can review the audit trail through the HTTP
API. Operators can query the `publication_events` table directly for
forensic analysis.