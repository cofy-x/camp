import type { AppDefinition } from '@cofy-x/camp-platinum';
import { Library } from './Library';

export const libraryApp: AppDefinition = {
  id: 'library',
  title: 'Library',
  icon: 'folder',
  component: Library,
  windowKey: () => 'main',
  defaultSize: { width: 1040, height: 650 },
  minSize: { width: 360, height: 320 },
};
