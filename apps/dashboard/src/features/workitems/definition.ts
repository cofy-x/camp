import type { AppDefinition } from '@cofy-x/camp-platinum';
import { lazyApplication } from '../../shared/desktop/lazyApplication';

const Work = lazyApplication(
  'src/features/workitems/Work.tsx',
  () => import('./Work'),
  (module) => module.Work,
);

export const workApp: AppDefinition = {
  id: 'work',
  title: 'Work',
  icon: 'clipboard',
  component: Work,
  windowKey: () => 'main',
  defaultSize: { width: 940, height: 650 },
  minSize: { width: 360, height: 320 },
};
