import { Avatar, PixelIcon } from '@cofy-x/camp-platinum';

export function IdentityAvatar({
  avatar,
  kind,
  size = 32,
}: {
  avatar?: string;
  kind: 'member' | 'agent';
  size?: number;
}) {
  return (
    <Avatar
      src={avatar ? `/api/avatars/${encodeURIComponent(avatar)}` : undefined}
      size={size}
    >
      <PixelIcon
        name={kind === 'agent' ? 'agent' : 'computer'}
        size={Math.max(16, size - 4)}
      />
    </Avatar>
  );
}
