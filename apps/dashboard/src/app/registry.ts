import type { AppDefinition } from '@cofy-x/camp-platinum';
import { aboutApp } from '../features/welcome/definition';
import { workspaceApp } from '../features/workspace/definition';
import { libraryApp } from '../features/library/definition';
import { runnersApp } from '../features/runners/definition';
import { agentsApp } from '../features/agents/definition';
import { settingsApp } from '../features/settings/definition';
import { workApp } from '../features/workitems/definition';
import { lazyApplication } from '../shared/desktop/lazyApplication';

const galleryApp: AppDefinition | undefined = import.meta.env.DEV
  ? {
      id: 'gallery',
      title: 'Platinum Controls',
      icon: 'gallery',
      component: lazyApplication(
        'src/features/gallery/Gallery.tsx',
        () => import('../features/gallery/Gallery'),
        (module) => module.Gallery,
      ),
      defaultSize: { width: 440, height: 460 },
      minSize: { width: 310, height: 310 },
    }
  : undefined;

export function createAppRegistry(): AppDefinition[] {
  return [
    workspaceApp,
    workApp,
    libraryApp,
    agentsApp,
    runnersApp,
    settingsApp,
    aboutApp,
    ...(galleryApp ? [galleryApp] : []),
  ];
}
