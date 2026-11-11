import type { AppDefinition } from '@cofy-x/camp-platinum';
import { lazyApplication } from '../../shared/desktop/lazyApplication';

const Gallery = lazyApplication(
  'src/features/gallery/Gallery.tsx',
  () => import('./Gallery'),
  (module) => module.Gallery,
);

export const galleryApp: AppDefinition = {
  id: 'gallery',
  title: 'Platinum Controls',
  icon: 'gallery',
  component: Gallery,
  defaultSize: { width: 440, height: 460 },
  minSize: { width: 310, height: 310 },
};
