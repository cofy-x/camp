import { StatusBar, type AppWindowProps } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { RunnersPanel } from './RunnersPanel';

export function Runners({ desktop }: AppWindowProps) {
  const { user } = useAccount();
  return (
    <div className="camp-runners-app">
      <RunnersPanel desktop={desktop} />
      <StatusBar>
        <span>{user.name}</span>
      </StatusBar>
    </div>
  );
}
