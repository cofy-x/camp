import assert from 'node:assert/strict';

// Qualification only: configure checks before dispatching the restart task.
export async function configureRestartResource(api, resourceId, environment) {
  const route = `/api/resources/${resourceId}`;
  const revision = (await api(route))?.resource?.revision;
  assert.ok(
    Number.isSafeInteger(revision) && revision > 0,
    'Read a current positive resource revision before configuring restart checks.',
  );
  return api(route, 'PATCH', {
    expectedRevision: revision,
    environment,
  });
}
