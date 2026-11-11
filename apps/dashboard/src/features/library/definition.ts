import type { AppDefinition } from '@cofy-x/camp-platinum';
import { lazyApplication } from '../../shared/desktop/lazyApplication';

const Library = lazyApplication(
  'src/features/library/Library.tsx',
  () => import('./Library'),
  (module) => module.Library,
);

export const libraryApp: AppDefinition = {
  id: 'library',
  title: 'Library',
  icon: 'folder',
  component: Library,
  windowKey: () => 'main',
  defaultSize: { width: 1040, height: 650 },
  minSize: { width: 360, height: 320 },
};
