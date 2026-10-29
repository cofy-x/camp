import type { DesktopAPI, DesktopIcon, MenuItem } from '@cofy-x/camp-platinum';

export function createDesktopIcons(): DesktopIcon[] {
  return [
    {
      id: 'system:workspace',
      label: 'Workspace',
      icon: 'conversation',
      appId: 'workspace',
    },
    {
      id: 'system:library',
      label: 'Library',
      icon: 'folder',
      appId: 'library',
    },
    { id: 'system:agents', label: 'Agents', icon: 'agent', appId: 'agents' },
    {
      id: 'system:runners',
      label: 'Runners',
      icon: 'computer',
      appId: 'runners',
    },
    {
      id: 'system:settings',
      label: 'Settings',
      icon: 'settings',
      appId: 'settings',
    },
  ];
}

export function createDesktopMenu(desktop: DesktopAPI): MenuItem[] {
  return [
    { label: 'Open Workspace', action: () => desktop.openApp('workspace') },
    { label: 'Open Library', action: () => desktop.openApp('library') },
    { label: 'Manage Agents', action: () => desktop.openApp('agents') },
    { label: 'Manage Runners', action: () => desktop.openApp('runners') },
    { separator: true },
    ...(import.meta.env.DEV
      ? [
          {
            label: 'Platinum Controls…',
            action: () => desktop.openApp('gallery'),
          },
        ]
      : []),
    { label: 'Clean Up Desktop', action: desktop.arrangeIcons },
    { label: 'Settings…', action: () => desktop.openApp('settings') },
  ];
}
