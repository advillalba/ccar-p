import http from 'node:http';

const note = {
  id: 'n1', slug: 'long-note', title: 'Long note', summary: 'Summary',
  domain: { id: 'd1', slug: 'security', name: 'Security', weight: 20, sort_order: 1 },
  tags: [], references: [],
  html: `<h2>Safe heading</h2><p>&lt;script&gt;window.leaked=true&lt;/script&gt;</p><pre><code>https://example.test/${'segment'.repeat(100)}</code></pre>`,
  reading_time_minutes: 2, published_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', previous: null, next: { slug: 'outro', title: 'Outro note' }
};
const bookedNote = {
  id: 'n2', slug: 'booked', title: 'Booked note', summary: 'Summary',
  domain: { id: 'd1', slug: 'security', name: 'Security', weight: 20, sort_order: 1 },
  tags: [], references: [],
  html: '<h2 id="first">First section</h2><p>First section body.</p><h2 id="second">Second section</h2><p>Second section body.</p><h2 id="third">Third section</h2><p>Third section body.</p>',
  toc: [
    { id: 'first', text: 'First section', level: 2 },
    { id: 'second', text: 'Second section', level: 2 },
    { id: 'third', text: 'Third section', level: 2 }
  ],
  reading_time_minutes: 3, published_at: '2026-02-01T00:00:00Z', updated_at: '2026-02-01T00:00:00Z',
  previous: { slug: 'long-note', title: 'Long note' }, next: { slug: 'outro', title: 'Outro note' }
};
const outroNote = {
  id: 'n3', slug: 'outro', title: 'Outro note', summary: 'Summary',
  domain: { id: 'd1', slug: 'security', name: 'Security', weight: 20, sort_order: 1 },
  tags: [], references: [],
  html: '<p>Final remarks.</p>',
  reading_time_minutes: 1, published_at: '2026-03-01T00:00:00Z', updated_at: '2026-03-01T00:00:00Z',
  previous: { slug: 'booked', title: 'Booked note' }, next: null
};
const pagedNote = {
  id: 'n4', slug: 'paged', title: 'Paged note', summary: 'Summary',
  domain: { id: 'd1', slug: 'security', name: 'Security', weight: 20, sort_order: 1 },
  tags: [], references: [],
  html: '<h2 id="top">Top</h2>' + Array.from({ length: 80 }, (_, i) => `<p>Paragraph ${i + 1} with enough body text to fill several reading pages.</p>`).join(''),
  toc: [{ id: 'top', text: 'Top', level: 2 }],
  reading_time_minutes: 5, published_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z',
  previous: { slug: 'booked', title: 'Booked note' }, next: { slug: 'outro', title: 'Outro note' }
};
const exam = {
  id: 'e1', slug: 'confidential', title: 'Confidential exam', description: 'No key', difficulty: 'medium', time_limit_minutes: 30, pass_percentage: 70,
  questions: [
    {
      id: 'q1-sample', prompt: 'Sample practice question one', difficulty: 'beginner', position: 1,
      references: [],
      options: [
        { id: 'q1-a', key: 'A', text: 'Answer one', position: 1 },
        { id: 'q1-b', key: 'B', text: 'Answer two', position: 2 }
      ]
    },
    {
      id: 'q2-sample', prompt: 'Sample practice question two', difficulty: 'beginner', position: 2,
      references: [],
      options: [
        { id: 'q2-a', key: 'A', text: 'Answer one', position: 1 },
        { id: 'q2-b', key: 'B', text: 'Answer two', position: 2 }
      ]
    },
    {
      id: 'q3-sample', prompt: 'Sample practice question three', difficulty: 'beginner', position: 3,
      references: [],
      options: [
        { id: 'q3-a', key: 'A', text: 'First answer', position: 1 },
        { id: 'q3-b', key: 'B', text: 'Second answer', position: 2 },
        { id: 'q3-c', key: 'C', text: 'Third answer', position: 3 }
      ]
    }
  ]
};
const correctOptionsByQuestion = {
  'q1-sample': ['q1-b'],
  'q2-sample': ['q2-b'],
  'q3-sample': ['q3-b', 'q3-c']
};
const send = (response, status, data, headers = {}) => {
  response.writeHead(status, { 'content-type': 'application/json', ...headers });
  response.end(JSON.stringify(data));
};
http.createServer((request, response) => {
  if (request.url === '/api/v1/auth/csrf') return send(response, 200, { data: { csrf_token: 'anonymous-token' } }, { 'set-cookie': 'ccarp_csrf=anonymous-token; Path=/' });
  if (request.url === '/api/v1/notes') return send(response, 200, { data: { notes: [] } });
  if (request.url === '/api/v1/domains') return send(response, 200, { data: { domains: [{ id: 'd1', slug: 'security', name: 'Security', weight: 20, sort_order: 1 }] } });
  if (request.url === '/api/v1/notes/long-note') return send(response, 200, { data: { note } });
  if (request.url === '/api/v1/notes/booked') return send(response, 200, { data: { note: bookedNote } });
  if (request.url === '/api/v1/notes/outro') return send(response, 200, { data: { note: outroNote } });
  if (request.url === '/api/v1/notes/paged') return send(response, 200, { data: { note: pagedNote } });
  if (request.url === '/api/v1/exams') return send(response, 200, { data: { exams: [{ id: 'e2', slug: 'sample', title: 'Sample exam', description: 'Sample practice exam', difficulty: 'beginner', time_limit_minutes: 30, pass_percentage: 70, question_count: 3, domains: [], published_at: '2026-04-01T00:00:00Z' }] } });
  if (request.url === '/api/v1/exams/confidential') return send(response, 200, { data: { exam } });
  if (request.url === '/api/v1/exams/sample') return send(response, 200, { data: { exam: { ...exam, slug: 'sample', title: 'Sample exam' } } });
  if (request.url === '/api/v1/exams/sample/practice' && request.method === 'POST') {
    let raw = '';
    request.on('data', chunk => { raw += chunk; });
    request.on('end', () => {
      let body = {};
      try { body = JSON.parse(raw || '{}'); } catch { body = {}; }
      if (!body || typeof body !== 'object') body = {};
      const question = exam.questions.find(item => item.id === body.question_id);
      if (!question) return send(response, 404, { error: { code: 'question_not_in_exam', message: 'question does not belong to this exam' } });
      const requested = Array.isArray(body.selected_option_ids) && body.selected_option_ids.length
        ? body.selected_option_ids
        : (body.selected_option_id ? [body.selected_option_id] : []);
      if (requested.length === 0) return send(response, 400, { error: { code: 'invalid_request', message: 'at least one selected option is required' } });
      const resolved = [];
      for (const id of requested) {
        const option = question.options.find(item => item.id === id || item.key === id);
        if (!option) return send(response, 422, { error: { code: 'option_not_in_question', message: 'selected option does not belong to question' } });
        if (!resolved.includes(option.id)) resolved.push(option.id);
      }
      const correct = (correctOptionsByQuestion[question.id] ?? []).slice().sort();
      const selectedSorted = resolved.slice().sort();
      const isCorrect = selectedSorted.length === correct.length && selectedSorted.every((id, index) => id === correct[index]);
      send(response, 200, {
        data: {
          id: question.id,
          position: question.position,
          prompt: question.prompt,
          difficulty: question.difficulty,
          explanation: 'Sample explanation: the highlighted option is the correct one.',
          options: question.options.map(item => ({ ...item, explanation: `Sample explanation for option ${item.key}.` })),
          references: [],
          selected_option_ids: resolved,
          correct_option_ids: correct,
          is_correct: isCorrect
        }
      });
    });
    return;
  }
  if (request.url?.startsWith('/api/v1/practice/questions')) {
    const difficulty = new URL(request.url, 'http://x').searchParams.get('difficulty') ?? '';
    const questions = difficulty && difficulty !== 'beginner' ? [] : exam.questions.map(question => ({
      id: question.id,
      exam_slug: 'sample',
      prompt: question.prompt,
      difficulty: 'beginner',
      position: question.position,
      options: question.options.map(option => ({ id: option.id, key: option.key, text: option.text, position: option.position }))
    }));
    return send(response, 200, { data: { questions } });
  }
  if (request.url?.startsWith('/api/v1/admin/') || request.url?.startsWith('/api/v1/attempts')) return send(response, 401, { error: { code: 'authentication_required', message: 'Authentication is required.' } });
  send(response, 404, { error: { code: 'not_found', message: 'Not found.' } });
}).listen(19091, '127.0.0.1');
