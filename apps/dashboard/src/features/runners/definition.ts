import type { AppDefinition } from '@cofy-x/camp-platinum';
import { lazyApplication } from '../../shared/desktop/lazyApplication';

const Runners = lazyApplication(
  'src/features/runners/Runners.tsx',
  () => import('./Runners'),
  (module) => module.Runners,
);

export const runnersApp: AppDefinition = {
  id: 'runners',
  title: 'Runners',
  icon: 'computer',
  component: Runners,
  windowKey: () => 'main',
  defaultSize: { width: 820, height: 650 },
  minSize: { width: 360, height: 320 },
};
