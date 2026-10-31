// Canonical change policy. The standalone workspace pins a byte-identical copy.
// No dependencies or machine configuration: callers supply verified Git inputs.
export const policyVersion = 1;

export function parseNameStatus(value) {
  if (!value) return [];
  const fields = value.split('\0');
  if (fields.pop() !== '') throw new Error('Truncated Git name-status output.');
  const changes = [];
  for (let i = 0; i < fields.length;) {
    const status = fields[i++];
    if (!/^(?:[ADMT]|[RC]\d{1,3})$/.test(status))
      throw new Error('Unsupported Git change status.');
    const paths = fields.slice(i, i + (/^[RC]/.test(status) ? 2 : 1));
    i += paths.length;
    if (
      paths.length !== (/^[RC]/.test(status) ? 2 : 1) ||
      paths.some((p) => !p || p.startsWith('/') || p.split('/').includes('..'))
    )
      throw new Error('Invalid Git change paths.');
    changes.push({ status, paths });
  }
  return changes;
}

export function documentationPath(name) {
  return (
    ['README.md', 'CONTRIBUTING.md', 'SECURITY.md', 'AGENTS.md'].includes(
      name,
    ) ||
    /^docs\/.+\.md$/.test(name) ||
    /^(?:apps|packages|tools|examples)\/.+\/(?:README|AGENTS)\.md$/.test(name)
  );
}

export function classifyChanges(
  changes,
  { repository = 'camp', uncertain } = {},
) {
  const paths = [...new Set(changes.flatMap((c) => c.paths))].sort();
  const governance = paths.some(
    (p) => /(?:^|\/)AGENTS\.md$/.test(p) || p === 'CONTRIBUTING.md',
  );
  const docs = paths.length > 0 && paths.every(documentationPath);
  const checks = {
    documentation: paths.some(documentationPath),
    source: !docs,
    browser: !docs,
    fixture: !docs,
    server: !docs,
    native: false,
    checkpoint: false,
    workspace: repository === 'workspace' && !docs,
    workflows: paths.some((p) => p.startsWith('.github/')),
  };
  const policy = paths.some(
    (p) =>
      /^\.github\//.test(p) ||
      /^scripts\/(?:change-policy|classify-changes|validation-gate)(?:\.|-)/.test(
        p,
      ) ||
      p === 'workspace.json',
  );
  const native =
    /^(?:apps\/server\/(?:internal\/(?:processowner|seatbelt|nativejob|nativeexecutor|cli|daemon|execution|projectenv|dsh|runnertools|sandboxexec|workspace|releaseinfo)|cmd\/camp-sandbox)|tools\/(?:project-runtime|dsh-runtime|seatbelt-runtime|layerfs-worker)|internal\/launcher|tools\/release-archive)\//;
  checks.native =
    !docs &&
    paths.some(
      (p) =>
        native.test(p) ||
        /^scripts\/(?:package-|release\.|smoke-bundle\.|qualify-native-mac\.)/.test(
          p,
        ) ||
        p === 'Makefile',
    );
  checks.checkpoint =
    !docs && paths.some((p) => /^tools\/layerfs-runtime\//.test(p));
  const unknown = paths.some(
    (p) =>
      !documentationPath(p) &&
      !(repository === 'workspace'
        ? /^(?:scripts\/|test\/|bin\/|\.hooks\/|workspace\.json$|\.gitignore$)/.test(
            p,
          )
        : /^(?:apps\/|packages\/|tools\/|scripts\/|internal\/|examples\/|\.github\/|Formula\/|[^/]+\.(?:go|mod|sum|json|yaml|yml)|Makefile$|Dockerfile$|\.[^/]+$)/.test(
            p,
          )),
  );
  const mixed = paths.some(documentationPath) && !docs;
  const full = Boolean(
    uncertain || policy || paths.length === 0 || unknown || mixed,
  );
  if (full) {
    for (const key of [
      'documentation',
      'source',
      'browser',
      'fixture',
      'server',
      'native',
      'checkpoint',
    ])
      checks[key] = true;
  }
  if (repository === 'workspace') {
    for (const key of [
      'source',
      'browser',
      'fixture',
      'server',
      'native',
      'checkpoint',
    ])
      checks[key] = false;
    checks.workspace = !docs || full;
  }
  return {
    schema: 1,
    policyVersion,
    repository,
    classification: full ? 'full' : docs ? 'documentation' : 'code',
    documentationOnly: docs && !full,
    governanceReview: governance || policy,
    changes,
    paths,
    checks,
    reasons: [
      ...(uncertain ? [uncertain] : []),
      ...(paths.length === 0
        ? ['No reliable nonempty diff; use complete validation.']
        : []),
      ...(policy
        ? ['Workflow or policy code changed; use complete validation.']
        : []),
      ...(unknown ? ['Unrecognized paths require complete validation.'] : []),
      ...(mixed
        ? ['Mixed documentation/code changes require complete validation.']
        : []),
      ...(docs ? ['Only recognized documentation paths changed.'] : []),
      ...(governance
        ? ['Instruction/process rules require behavioral review.']
        : []),
      ...(!docs && !full
        ? [
            'Code, build, dependency or unrecognized paths require source/integration validation.',
          ]
        : []),
    ],
  };
}

export function gateDecision(required, result, classificationResult) {
  if (classificationResult !== 'success')
    return { passed: false, reason: 'Change classification did not succeed.' };
  if (required === 'true')
    return {
      passed: result === 'success',
      reason: `Required validation: ${result}.`,
    };
  if (required === 'false')
    return {
      passed: result === 'skipped',
      reason: `Outside change scope; validation must be skipped, observed ${result}.`,
    };
  return { passed: false, reason: 'Missing or invalid scope decision.' };
}

export function validationStage(plan, stage = 'merge') {
  if (stage !== 'release') return { ...plan, stage };
  return {
    ...plan,
    stage,
    documentationOnly: false,
    checks: {
      ...plan.checks,
      documentation: true,
      source: true,
      browser: true,
      fixture: true,
      // Release already qualifies the exact Mac archives and native runtimes.
      server: false,
      native: false,
      checkpoint: false,
    },
    reasons: [
      ...plan.reasons,
      'Release always runs complete core regression before native/package qualification.',
    ],
  };
}

export function localProfile(plan, { manifestTestsOnly = false } = {}) {
  if (plan.documentationOnly) return 'documentation';
  if (plan.repository === 'workspace') return 'workspace';
  const workflowOnly =
    plan.paths.length > 0 &&
    plan.paths.every(
      (name) =>
        documentationPath(name) ||
        name.startsWith('.github/workflows/') ||
        /^scripts\/(?:change-policy|classify-changes|ci-policy|check-portable-go)(?:\.|-)/.test(
          name,
        ) ||
        (name === 'package.json' && manifestTestsOnly),
    );
  return workflowOnly ? 'workflow' : 'source';
}

export const sourceProfiles = {
  mac: {
    goPackageParallelism: 2,
    linuxOnlyGoPackages: [
      {
        package: 'github.com/cofy-x/camp/apps/server/internal/layerfs',
        reason:
          'The LayerFS bridge tests require Linux and run in Linux source checks; Mac native/worker qualification remains separate.',
      },
    ],
  },
};

export function portableGoPlan(packages) {
  const profile = sourceProfiles.mac;
  if (
    !packages.length ||
    !packages.every(
      (name) =>
        name === 'github.com/cofy-x/camp' ||
        name.startsWith('github.com/cofy-x/camp/'),
    )
  )
    throw new Error('Expected the actual Camp Go package list.');
  for (const row of profile.linuxOnlyGoPackages)
    if (!packages.includes(row.package))
      throw new Error(`Stale platform exclusion: ${row.package}`);
  return {
    parallelism: profile.goPackageParallelism,
    exclusions: profile.linuxOnlyGoPackages,
    args: [
      'test',
      '-race',
      '-p',
      String(profile.goPackageParallelism),
      ...packages.filter(
        (name) =>
          !profile.linuxOnlyGoPackages.some((row) => row.package === name),
      ),
    ],
  };
}
