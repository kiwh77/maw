// Only authoritative task states determine cargo placement. Animation never completes work.
export function cargoZone(status) {
  if (status === 'done') return 'stored';
  if (status === 'cancelled') return 'cancelled';
  if (status === 'blocked' || status === 'conflict') return 'blocked';
  if (['claimed', 'in_progress', 'self_check'].includes(status)) return 'processing';
  if (['in_review', 'merging'].includes(status)) return 'inspection';
  if (['ready', 'backlog'].includes(status)) return 'queued';
  return 'unknown';
}
export function factoryGroups(tasks) {
  const groups = new Map();
  for (const task of tasks) {
    const id = task.project_id || '';
    if (!groups.has(id)) groups.set(id, { id, name: task.project_name || id || '—', tasks: [], counts: {} });
    const g = groups.get(id), zone = cargoZone(task.status);
    g.tasks.push(task); g.counts[zone] = (g.counts[zone] || 0) + 1;
  }
  for (const g of groups.values()) {
    g.complete = g.tasks.length > 0 && g.counts.stored === g.tasks.length;
    g.size = g.tasks.length <= 5 ? 'small' : g.tasks.length <= 20 ? 'medium' : 'large';
  }
  return [...groups.values()];
}
