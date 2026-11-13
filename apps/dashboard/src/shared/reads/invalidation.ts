import type { WorkspaceEvent } from '../../features/workspace/api';

export function affectedRead(path: string, event: WorkspaceEvent): boolean {
  const url = new URL(path, 'http://camp');
  const route = url.pathname;
  const type = event.type;
  const [, family, encodedEntity] = route.split('/');
  const entity = encodedEntity ? decodeURIComponent(encodedEntity) : undefined;
  const room = family === 'rooms' ? entity : undefined;
  const run = family === 'runs' ? entity : undefined;
  const task = family === 'tasks' ? entity : undefined;
  const work = family === 'work-items' ? entity : undefined;
  const review = family === 'reviews' ? entity : undefined;
  if (type === 'personal_read.updated')
    return route === '/me/inbox' || route === '/me/room-reads';
  if (type === 'team.changed' || type === 'management.updated') return true;
  if (type === 'run.activity')
    return Boolean(
      run === event.entityId &&
      route.endsWith('/activity') &&
      !url.searchParams.has('through'),
    );
  if (run && route.endsWith('/activity') && url.searchParams.has('through'))
    return false;
  if (
    run &&
    url.searchParams.get('view') === 'summary' &&
    (type === 'runner.updated' || type === 'run.finished')
  )
    return true;
  if (
    route === '/team' ||
    route === '/team/members' ||
    route === '/me/room-reads'
  )
    return false;
  if (route === '/me/inbox' || route === '/attention')
    return (
      type === 'room.updated' ||
      (route === '/attention' && type === 'resource.updated') ||
      /^(message\.|task\.|work_item\.|review\.|automation\.delivery\.|run\.finished)/.test(
        type,
      )
    );
  if (route === '/rooms')
    return /^(room\.|message\.|membership\.|task\.|run\.(queued|finished)|resource\.)/.test(
      type,
    );
  if (route === '/agents') return type === 'agent.updated';
  if (/^\/agents\/[^/]+\/rooms$/.test(route))
    return type === 'membership.updated' && entity === event.entityId;
  if (route === '/resources' || route === '/library')
    return /^(room\.updated|resource\.updated|source_import\.updated|upload\.created)$/.test(
      type,
    );
  if (family === 'resources' && entity) {
    if (url.searchParams.has('commit')) return false;
    return (
      entity === event.entityId &&
      (type === 'resource.updated' || type === 'upload.created')
    );
  }
  if (route.startsWith('/runners/') && route.endsWith('/sources'))
    return type === 'runner.updated' || type === 'source_import.updated';
  if (route.startsWith('/runners') || route.startsWith('/settings/'))
    return /^(runner\.|agent\.|execution_defaults\.|room\.)/.test(type);
  if (route === '/workspace/status')
    return /^(runner\.|agent\.|run\.(queued|updated|finished)|membership\.)/.test(
      type,
    );
  if (route === '/execution-defaults')
    return type === 'execution_defaults.updated' || type === 'runner.updated';
  if (route === '/runner-enrollments')
    return type === 'runner.updated' || type === 'runner_enrollment.updated';
  if (route.startsWith('/source-imports'))
    return (
      type === 'source_import.updated' &&
      (route === '/source-imports' || entity === event.entityId)
    );
  if (run) return run === event.entityId && type.startsWith('run.');
  if (task && route.endsWith('/subscriptions'))
    return (
      (task === event.entityId && type === 'task.updated') ||
      type === 'automation.policy.updated' ||
      type === 'automation.event.updated'
    );
  if (task && route.endsWith('/deliveries'))
    return (
      type === 'automation.delivery.updated' ||
      (task === event.entityId && type === 'task.updated')
    );
  if (task)
    return (
      (task === event.entityId && type === 'task.updated') ||
      (type.startsWith('run.') && type !== 'run.activity')
    );
  if (work) return type === 'work_item.updated' && work === event.entityId;
  if (route === '/work-items')
    return (
      (!url.searchParams.get('roomId') ||
        url.searchParams.get('roomId') === event.roomId) &&
      /^(room\.updated|work_item\.|task\.|run\.(queued|updated|finished))/.test(
        type,
      )
    );
  if (review)
    return (
      (type === 'review.updated' && review === event.entityId) ||
      type === 'resource.updated'
    );
  if (route === '/reviews')
    return (
      (!url.searchParams.get('roomId') ||
        !event.roomId ||
        url.searchParams.get('roomId') === event.roomId) &&
      (type === 'review.updated' ||
        (type === 'resource.updated' &&
          (!url.searchParams.get('resourceId') ||
            url.searchParams.get('resourceId') === event.entityId)))
    );
  if (room && route.endsWith('/work-item-defaults'))
    return (
      (!event.roomId || room === event.roomId) &&
      /^(room\.updated|membership\.updated|agent\.updated|runner\.updated|execution_defaults\.updated|automation\.policy\.updated)$/.test(
        type,
      )
    );
  if (room) {
    if (event.roomId && room !== event.roomId) return false;
    if (route.endsWith('/members'))
      return type === 'membership.updated' || type === 'agent.updated';
    if (route.endsWith('/messages')) return type === 'message.created';
    if (route.endsWith('/runs'))
      return type.startsWith('run.') && type !== 'run.activity';
    if (route.endsWith('/tasks')) return type === 'task.updated';
    if (route.includes('/work-item'))
      return (
        type === 'work_item.updated' || type === 'automation.policy.updated'
      );
    if (route.includes('/memories')) return type === 'memory.updated';
    if (route.includes('/github')) return type.startsWith('github.');
    if (route.includes('/automation-policies'))
      return type === 'automation.policy.updated';
    if (route.includes('/automation')) return type.startsWith('automation.');
    if (route.includes('/uploads')) return type === 'upload.created';
    return type === 'room.updated' || type === 'resource.updated';
  }
  if (route.startsWith('/automation-evaluations/'))
    return (
      type === 'automation.evaluation.updated' && entity === event.entityId
    );
  if (route.startsWith('/automation-events/'))
    return type === 'automation.event.updated' && entity === event.entityId;
  if (route.startsWith('/automation-deliveries/'))
    return type === 'automation.delivery.updated';
  if (route.startsWith('/github-events/'))
    return type.startsWith('github.event.') && entity === event.entityId;
  if (route.startsWith('/github-sources/'))
    return type === 'github.source.updated' && entity === event.entityId;
  if (route.startsWith('/memories/'))
    return (
      !url.searchParams.has('revision') &&
      type === 'memory.updated' &&
      entity === event.entityId
    );
  return false;
}
