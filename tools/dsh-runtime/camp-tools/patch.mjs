/** Public sdk-minimal overlay: all model file and command work uses Camp. */
export function toolPatch(access, moduleRoot = '/runtime/camp-tools') {
  if (!['read', 'write'].includes(access))
    throw new Error('Camp tool access must be read or write.');
  const write = access === 'write';
  return [
    ...[
      'filesystem',
      'subprocess',
      'pty',
      'terminal-bash',
      'terminal-pwsh',
      'persistent-bash',
      'persistent-pwsh',
      'jobs',
      'tool-working-directory',
    ].map((id) => ({ id, disabled: true })),
    {
      id: 'tool-fs',
      config: { read: true, readImage: false, write, edit: write },
    },
    {
      insert: [
        { id: 'camp-filesystem', name: `${moduleRoot}/filesystem.mjs` },
        { id: 'camp-file-discovery', name: `${moduleRoot}/discovery.mjs` },
        ...(write
          ? [
              {
                id: 'fs-observation-policy',
                name: '@deepseek-ai/dsh-fs-observation-policy',
              },
              { id: 'camp-shell', name: `${moduleRoot}/shell.mjs` },
              { id: 'shell-env', name: '@deepseek-ai/dsh-shell-env' },
              {
                id: 'camp-bash',
                name: '@deepseek-ai/dsh-tool-bash',
                config: {
                  enableRunInBackground: false,
                  promoteOnTimeout: false,
                },
              },
            ]
          : []),
      ],
    },
  ];
}
