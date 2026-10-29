import type { AppDefinition } from '@cofy-x/camp-platinum';
import { aboutApp } from '../features/welcome/definition';
import { workspaceApp } from '../features/workspace/definition';
import { libraryApp } from '../features/library/definition';
import { runnersApp } from '../features/runners/definition';
import { agentsApp } from '../features/agents/definition';
import { settingsApp } from '../features/settings/definition';
import { galleryApp } from '../features/gallery/definition';

if (import.meta.env.DEV) void import('../features/gallery/gallery.css');

export function createAppRegistry(): AppDefinition[] {
  return [
    workspaceApp,
    libraryApp,
    agentsApp,
    runnersApp,
    settingsApp,
    aboutApp,
    ...(import.meta.env.DEV ? [galleryApp] : []),
  ];
}
