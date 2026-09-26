CREATE TABLE practice_answers (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_id uuid NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    selected_option_ids uuid[] NOT NULL,
    is_correct boolean NOT NULL,
    answered_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, question_id)
);
CREATE INDEX practice_answers_user_idx ON practice_answers(user_id, answered_at DESC);
