import { useState, useEffect } from 'preact/hooks';
import { correctAnswerLine, toggleSelection } from './answers';
import './exam-runner.css';

export interface RunnerOption {
  id: string;
  key: string;
  text: string;
  position: number;
  explanation?: string;
}

export type RunnerReference = { title: string; url?: string; citation?: string };

export interface RunnerQuestion {
  id: string;
  prompt: string;
  scenario?: string;
  options: RunnerOption[];
  selectedOptionIds?: string[];
  isCorrect?: boolean | null;
  correctOptionIds?: string[];
  references?: RunnerReference[];
}

export interface RunnerFeedback {
  is_correct: boolean;
  selected_option_ids: string[];
  correct_option_ids?: string[];
  correctOptionIds?: string[];
  options: RunnerOption[];
  explanation?: string;
  references?: RunnerReference[];
}

interface ExamRunnerProps {
  attemptId: string;
  questions: RunnerQuestion[];
  csrf: string;
}

interface AnswerEntry {
  selectedOptionIds: string[];
  feedback?: RunnerFeedback;
}

const errorFromPayload = (payload: unknown): string => {
  if (payload && typeof payload === 'object' && 'error' in payload) {
    const error = (payload as { error?: unknown }).error;
    if (error && typeof error === 'object' && 'message' in error) {
      const message = (error as { message?: unknown }).message;
      if (typeof message === 'string' && message) return message;
    }
  }
  return 'The request could not be completed.';
};

const feedbackFromPayload = (payload: unknown): RunnerFeedback | null => {
  if (!payload || typeof payload !== 'object') return null;
  const data = (payload as { data?: unknown }).data;
  for (const candidate of [data, payload]) {
    if (!candidate || typeof candidate !== 'object') continue;
    const nested = (candidate as { feedback?: unknown }).feedback;
    if (nested && typeof nested === 'object' && 'is_correct' in (nested as object)) return nested as RunnerFeedback;
    if ('is_correct' in (candidate as object)) return candidate as RunnerFeedback;
  }
  return null;
};

const resolveCsrf = (fallback: string): string => {
  try {
    return window.sessionStorage.getItem('ccarp-session-csrf') || fallback;
  } catch {
    return fallback;
  }
};

const feedbackIds = (feedback: RunnerFeedback | undefined): string[] => {
  if (!feedback) return [];
  if (Array.isArray(feedback.correct_option_ids)) return feedback.correct_option_ids;
  if (Array.isArray(feedback.correctOptionIds)) return feedback.correctOptionIds;
  return [];
};

export default function ExamRunner({ attemptId, questions, csrf }: ExamRunnerProps) {
  const [answers, setAnswers] = useState<Record<string, AnswerEntry>>(() => {
    const initial: Record<string, AnswerEntry> = {};
    for (const question of questions) {
      const previous = question.selectedOptionIds ?? [];
      if (previous.length === 0) continue;
      const feedback = question.isCorrect === undefined || question.isCorrect === null
        ? undefined
        : {
            is_correct: question.isCorrect,
            selected_option_ids: previous,
            correctOptionIds: question.correctOptionIds ?? [],
            options: question.options,
            references: question.references
          };
      initial[question.id] = { selectedOptionIds: previous, feedback };
    }
    return initial;
  });
  const [currentIdx, setCurrentIdx] = useState(() => {
    const index = questions.findIndex(question => (question.selectedOptionIds?.length ?? 0) === 0);
    return index === -1 ? 0 : index;
  });
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [showSubmit, setShowSubmit] = useState(false);
  const [submitBusy, setSubmitBusy] = useState(false);
  const [submitError, setSubmitError] = useState('');

  const question = questions[currentIdx];
  const total = questions.length;
  const answeredCount = Object.keys(answers).length;
  const unanswered = total - answeredCount;
  const entry = question ? answers[question.id] : undefined;

  useEffect(() => {
    setSelected([]);
    setError('');
  }, [currentIdx]);

  const checkAnswer = async () => {
    if (!question || selected.length === 0 || answers[question.id] || busy) return;
    setBusy(true);
    setError('');
    try {
      const response = await fetch(`/api/v1/attempts/${encodeURIComponent(attemptId)}/answers`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { accept: 'application/json', 'content-type': 'application/json', 'x-csrf-token': resolveCsrf(csrf) },
        body: JSON.stringify({ question_id: question.id, selected_option_ids: selected })
      });
      const payload: unknown = response.status === 204 ? null : await response.json().catch(() => null);
      if (!response.ok) throw new Error(errorFromPayload(payload));
      const graded = feedbackFromPayload(payload);
      if (!graded) throw new Error('The answer response is incomplete.');
      const chosen = (graded as { selected_option_ids?: string[] }).selected_option_ids?.length
        ? (graded as { selected_option_ids: string[] }).selected_option_ids
        : selected;
      setAnswers(previous => ({ ...previous, [question.id]: { selectedOptionIds: chosen, feedback: graded } }));
      if (answeredCount + 1 >= total) setShowSubmit(true);
    } catch (caught) {
      setError(caught instanceof Error && caught.message ? caught.message : 'The request could not be completed.');
    } finally {
      setBusy(false);
    }
  };

  const submitAttempt = async () => {
    if (submitBusy) return;
    setSubmitBusy(true);
    setSubmitError('');
    try {
      const response = await fetch(`/api/v1/attempts/${encodeURIComponent(attemptId)}/submit`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { accept: 'application/json', 'content-type': 'application/json', 'x-csrf-token': resolveCsrf(csrf) },
        body: JSON.stringify({})
      });
      const payload: unknown = response.status === 204 ? null : await response.json().catch(() => null);
      if (!response.ok) throw new Error(errorFromPayload(payload));
      window.location.assign(`/attempts/${encodeURIComponent(attemptId)}`);
    } catch (caught) {
      setSubmitError(caught instanceof Error && caught.message ? caught.message : 'The request could not be completed.');
      setSubmitBusy(false);
    }
  };

  if (!question) return null;

  return (
    <div class="exam-runner">
      <div class="exam-runner__statusbar card">
        <div class="exam-runner__counts">
          <p class="exam-runner__count">Question <span class="num">{currentIdx + 1}</span> of <span class="num">{total}</span></p>
          <p class="exam-runner__count"><span class="num">{answeredCount}</span> answered</p>
        </div>
        <div class="progress" role="progressbar" aria-valuenow={answeredCount} aria-valuemin={0} aria-valuemax={total} aria-label="Answered questions">
          <div class="progress__fill" style={`width: ${total ? (answeredCount / total) * 100 : 0}%`}></div>
        </div>
      </div>

      <nav aria-label="Question navigator">
        <div class="qnav">
          {questions.map((item, index) => {
            const state = answers[item.id];
            const classes = ['qnav__dot'];
            if (state) classes.push(state.feedback ? (state.feedback.is_correct ? 'qnav__dot--correct' : 'qnav__dot--incorrect') : 'qnav__dot--answered');
            return (
              <button
                type="button"
                key={item.id}
                class={classes.join(' ')}
                aria-label={`Question ${index + 1}, ${state ? 'answered' : 'not answered'}`}
                aria-current={index === currentIdx ? 'page' : undefined}
                onClick={() => {
                  setShowSubmit(false);
                  setCurrentIdx(index);
                }}
              >
                {index + 1}
              </button>
            );
          })}
        </div>
      </nav>

      {entry ? (
        <div class="exam-runner__graded" role="status" aria-live="polite">
          <article class="card" key={question.id}>
            {question.scenario && <p class="scenario">{question.scenario}</p>}
            <h2 class="prompt">{question.prompt}</h2>
            {!entry.feedback && <p class="badge badge--outline">Answered</p>}
            {entry.feedback && (
              <p class={`verdict ${entry.feedback.is_correct ? 'verdict--correct' : 'verdict--incorrect'}`}>{entry.feedback.is_correct ? 'Correct' : 'Incorrect'}</p>
            )}
            {entry.feedback && correctAnswerLine(question.options, feedbackIds(entry.feedback)) && (
              <p>{correctAnswerLine(question.options, feedbackIds(entry.feedback))}</p>
            )}
            <div class="options">
              {question.options.map(option => {
                const isCorrectOption = feedbackIds(entry.feedback).includes(option.id);
                const isSelectedOption = entry.selectedOptionIds.includes(option.id);
                const showExplanation = option.explanation && (isCorrectOption || (isSelectedOption && !entry.feedback?.is_correct));
                const classes = ['opt', 'opt--locked'];
                if (isCorrectOption) classes.push('opt--correct');
                if (isSelectedOption && !isCorrectOption) classes.push('opt--incorrect');
                return (
                  <div key={option.id} class={classes.join(' ')}>
                    <span class="opt__key" aria-hidden="true">{option.key}.</span>
                    <div class="exam-runner__option-body">
                      <span>{option.text}</span>
                      {showExplanation && <p class="opt__explanation">{option.explanation}</p>}
                    </div>
                    {isSelectedOption && !entry.feedback && <span class="badge badge--outline">Your answer</span>}
                  </div>
                );
              })}
            </div>
            {entry.feedback?.explanation && (
              <div class="callout">
                <strong>Explanation.</strong> {entry.feedback.explanation}
              </div>
            )}
            {entry.feedback?.references?.length ? (
              <div class="callout callout--muted">
                <strong>References.</strong>
                <ul>
                  {entry.feedback.references.map(reference => (
                    <li key={reference.title}>
                      <span>{reference.title}</span>
                      {reference.url && (
                        <>
                          {' — '}
                          <a href={reference.url} rel="noopener noreferrer">
                            {reference.url}
                          </a>
                        </>
                      )}
                      {reference.citation && <span> ({reference.citation})</span>}
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
          </article>
        </div>
      ) : (
        <article class="card" aria-busy={busy ? 'true' : undefined}>
          {question.scenario && <p class="scenario">{question.scenario}</p>}
          <h2 class="prompt">{question.prompt}</h2>
          <fieldset>
            <legend class="visually-hidden">Choose one or more options</legend>
            <ol class="options">
              {question.options.map(option => (
                <li key={option.id}>
                  <label class="opt">
                    <input
                      type="checkbox"
                      name={`question-${question.id}`}
                      value={option.id}
                      checked={selected.includes(option.id)}
                      onInput={() => setSelected(current => toggleSelection(current, option.id))}
                      disabled={busy}
                    />
                    <span class="opt__key" aria-hidden="true">{option.key}.</span>
                    <span class="exam-runner__option-body">{option.text}</span>
                  </label>
                </li>
              ))}
            </ol>
          </fieldset>
          <div class="btnrow">
            <button type="button" class="btn btn--primary" disabled={selected.length === 0 || busy} aria-busy={busy ? 'true' : undefined} onClick={checkAnswer}>
              {busy ? 'Checking…' : 'Check answer'}
            </button>
          </div>
          {error && (
            <div class="alert alert--error" role="alert">
              <p>
                <strong>We could not record that answer.</strong> {error} Your choice is still selected; press Check answer to retry.
              </p>
            </div>
          )}
        </article>
      )}

      <div class="btnrow">
        <button type="button" class="btn btn--secondary" disabled={currentIdx === 0} onClick={() => setCurrentIdx(currentIdx - 1)}>
          Previous
        </button>
        <button type="button" class="btn btn--secondary" disabled={currentIdx === total - 1} onClick={() => setCurrentIdx(currentIdx + 1)}>
          Next
        </button>
        <button type="button" class="btn btn--secondary" onClick={() => setShowSubmit(true)}>
          Review &amp; submit
        </button>
      </div>

      {showSubmit && (
        <section class="card" aria-labelledby="exam-submit-title">
          <h2 id="exam-submit-title">Submit attempt</h2>
          <p>
            You have answered <strong class="num">{answeredCount}</strong> of <strong class="num">{total}</strong> questions.
          </p>
          {unanswered > 0 && (
            <div class="callout callout--muted">
              {unanswered} question{unanswered === 1 ? ' is' : 's are'} unanswered. You can go back and answer, or submit as you
              are — unanswered questions count as incorrect.
            </div>
          )}
          <p>Once you submit you cannot change your answers; you will then see the correct answers and explanations.</p>
          {submitError && (
            <div class="alert alert--error" role="alert">
              <p>{submitError}</p>
            </div>
          )}
          <div class="btnrow">
            <button type="button" class="btn btn--primary" disabled={submitBusy} aria-busy={submitBusy ? 'true' : undefined} onClick={submitAttempt}>
              {submitBusy ? 'Submitting…' : 'Submit attempt'}
            </button>
            <button type="button" class="btn btn--secondary" onClick={() => setShowSubmit(false)}>Keep answering</button>
          </div>
        </section>
      )}

      <noscript>
        <div class="noscript-note">
          Saving answers and submitting this exam require JavaScript. You can still read <a href="/notes">study notes</a> and
          browse <a href="/exams">exam details</a>.
        </div>
      </noscript>
    </div>
  );
}
