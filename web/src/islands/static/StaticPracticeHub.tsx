interface StaticPracticeHubProps {
  counts: Record<string, number>;
  basePath: string;
}

const levels = [
  { slug: 'beginner', label: 'Beginner', pitch: 'Foundational questions to build confidence.', badge: 'badge--success' },
  { slug: 'intermediate', label: 'Intermediate', pitch: 'Core exam practice at regular pace.', badge: 'badge--warning' },
  { slug: 'advanced', label: 'Advanced', pitch: 'Hard questions that demand careful reasoning.', badge: 'badge--danger' },
  { slug: 'exam_scenarios', label: 'Exam Scenarios', pitch: 'Full exam simulations with realistic scenarios — the ultimate challenge.', badge: 'badge--exam-scenarios' }
];

// Rendered without a hydration directive: purely static difficulty listing.
export default function StaticPracticeHub({ counts, basePath }: StaticPracticeHubProps) {
  return (
    <nav class="list" aria-label="Practice difficulties">
      {levels.map(level => (
        <a class="list-row card--interactive" href={`${basePath}/practice/${level.slug}`}>
          <div class="list-row__body">
            <h2 class="card__title">{level.label}</h2>
            <div class="muted">{level.pitch}</div>
            <span class={`badge ${level.badge}`}>{counts[level.slug] ?? 0} questions</span>
          </div>
          <span class="list-row__chev" aria-hidden="true">&rsaquo;</span>
        </a>
      ))}
    </nav>
  );
}
