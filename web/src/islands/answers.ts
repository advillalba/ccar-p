export interface AnswerOptionLike {
  id: string;
  key: string;
}

export function correctAnswerLine(options: AnswerOptionLike[], correctOptionIds: string[]): string {
  const keys = correctOptionIds
    .map(id => options.find(option => option.id === id)?.key)
    .filter((key): key is string => Boolean(key));
  if (keys.length === 0) return '';
  if (keys.length === 1) return `The correct answer is ${keys[0]}.`;
  if (keys.length === 2) return `The correct answers are ${keys[0]} and ${keys[1]}.`;
  return `The correct answers are ${keys.slice(0, -1).join(', ')} and ${keys[keys.length - 1]}.`;
}

export function toggleSelection(current: string[], id: string): string[] {
  return current.includes(id) ? current.filter(value => value !== id) : [...current, id];
}
