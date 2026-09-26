import type { Domain, PracticeQuestion } from '../lib';

export type PracticeDifficulty = 'beginner' | 'intermediate' | 'advanced' | 'exam_scenarios';

export const practiceDifficulties: PracticeDifficulty[] = ['beginner', 'intermediate', 'advanced', 'exam_scenarios'];

interface DumpQuestion {
  id: string;
  exam_id: string;
  domain_id: string;
  prompt: string;
  scenario?: string;
  explanation?: string;
  difficulty: string;
  position: number;
}

interface DumpOption {
  id: string;
  question_id: string;
  option_key: string;
  text: string;
  is_correct: boolean;
  position: number;
  explanation?: string;
}

interface DumpDomain {
  id: string;
  name: string;
  slug: string;
  weight: number;
  sort_order: number;
  is_active: boolean;
}

// The public dump is part of the repo (web/../db/public-dump) and is inlined
// as raw strings by Vite's glob at build/dev time; the SSR container does not
// ship it, so failures there degrade to empty practice pages.
const dumpFiles = import.meta.glob('../../../db/public-dump/*.jsonl', { eager: true, import: 'default', query: '?raw' }) as Record<string, string>;

const readJsonLines = <T,>(file: string): T[] => {
  // Glob keys are relative to the pattern's common base (here the repo root,
  // because the glob escapes web/); the call sites pass those same paths.
  const contents = dumpFiles[file] ?? dumpFiles[`../../../db/public-dump/${file}`];
  if (contents === undefined) {
    console.warn(`[dump] Could not read ${file}`);
    return [];
  }
  try {
    return (String(contents)).split('\n').filter(line => line.trim().length > 0).map(line => JSON.parse(line) as T);
  } catch (error) {
    console.warn(`[dump] Could not parse ${file}:`, error instanceof Error ? error.message : error);
    return [];
  }
};

const dumpQuestions = readJsonLines<DumpQuestion>('../../../db/public-dump/questions.jsonl');
const dumpOptions = readJsonLines<DumpOption>('../../../db/public-dump/question_options.jsonl');
const dumpDomains = readJsonLines<DumpDomain>('../../../db/public-dump/domains.jsonl');

const optionsByQuestionId = new Map<string, DumpOption[]>();
for (const option of dumpOptions) {
  const existing = optionsByQuestionId.get(option.question_id);
  if (existing) existing.push(option);
  else optionsByQuestionId.set(option.question_id, [option]);
}
for (const options of optionsByQuestionId.values()) {
  options.sort((a, b) => a.position - b.position);
}

const toPracticeQuestion = (question: DumpQuestion): PracticeQuestion => ({
  id: question.id,
  domain_id: question.domain_id,
  prompt: question.prompt,
  scenario: question.scenario,
  difficulty: question.difficulty,
  position: question.position,
  explanation: question.explanation,
  options: (optionsByQuestionId.get(question.id) ?? []).map(option => ({
    id: option.id,
    key: option.option_key,
    text: option.text,
    position: option.position,
    explanation: option.explanation,
    is_correct: option.is_correct
  }))
});

export const allDomains: Domain[] = dumpDomains.map(domain => ({
  id: domain.id,
  name: domain.name,
  slug: domain.slug,
  weight: domain.weight,
  sort_order: domain.sort_order,
  is_active: domain.is_active
}));

const questionsByDifficulty: Record<PracticeDifficulty, PracticeQuestion[]> = {
  beginner: [],
  intermediate: [],
  advanced: [],
  exam_scenarios: []
};
for (const question of dumpQuestions) {
  const difficulty = practiceDifficulties.find(value => value === question.difficulty);
  if (!difficulty) continue;
  questionsByDifficulty[difficulty].push(toPracticeQuestion(question));
}
for (const difficulty of practiceDifficulties) {
  questionsByDifficulty[difficulty].sort((a, b) => a.position - b.position);
}

export const practiceQuestionsByDifficulty: Record<PracticeDifficulty, PracticeQuestion[]> = questionsByDifficulty;

export const countsByDifficulty: Record<PracticeDifficulty, number> = {
  beginner: questionsByDifficulty.beginner.length,
  intermediate: questionsByDifficulty.intermediate.length,
  advanced: questionsByDifficulty.advanced.length,
  exam_scenarios: questionsByDifficulty.exam_scenarios.length
};
