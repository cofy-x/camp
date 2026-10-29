import { useLayoutEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { APIError } from '../../shared/api/http';
import { teamApi, type User } from '../../team/api';
import { AvatarPicker } from './AvatarPicker';

export function AccountAvatar({
  user,
  onSaved,
}: {
  user: User;
  onSaved: () => void;
}) {
  const [selection, setSelection] = useState<{
    avatar: string;
    expected: string;
  } | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const [notice, setNotice] = useState('');
  const opener = useRef<HTMLButtonElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const restoreFocus = useRef(false);
  const focusChoice = useRef(false);
  useLayoutEffect(() => {
    if (pending) return;
    if (!selection && restoreFocus.current) {
      opener.current?.focus();
      restoreFocus.current = false;
    }
    if (selection && focusChoice.current) {
      form.current?.querySelector<HTMLInputElement>('input:checked')?.focus();
      focusChoice.current = false;
    }
  }, [pending, selection]);

  async function save() {
    if (!selection || pending) return;
    setPending(true);
    setError('');
    setConflict(false);
    setNotice('');
    try {
      await teamApi.saveAvatar(selection.avatar, selection.expected);
      setSelection(null);
      setNotice('Avatar saved.');
      onSaved();
    } catch (error) {
      setError(
        error instanceof Error ? error.message : 'Your avatar was not saved.',
      );
      setConflict(
        error instanceof APIError && error.code === 'profile_conflict',
      );
    } finally {
      setPending(false);
    }
  }

  async function reload() {
    setPending(true);
    setError('');
    try {
      const { user } = await teamApi.status();
      if (!user) throw new Error('Sign in to Camp.');
      focusChoice.current = true;
      setSelection({ avatar: user.avatar, expected: user.avatar });
      setConflict(false);
      onSaved();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not load your current avatar.',
      );
    } finally {
      setPending(false);
    }
  }

  return selection ? (
    <form
      ref={form}
      className="camp-account-avatar"
      onKeyDown={(event) => {
        if (event.key === 'Escape' && !pending) {
          event.preventDefault();
          event.stopPropagation();
          setSelection(null);
          setError('');
        }
      }}
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <AvatarPicker
        kind="member"
        value={selection.avatar}
        onChange={(avatar) => {
          setSelection({ ...selection, avatar });
          setError('');
        }}
        disabled={pending}
      />
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      <div className="app-actions">
        <Button
          primary
          type="submit"
          disabled={pending || selection.avatar === selection.expected}
        >
          {pending ? 'Saving…' : 'Save Avatar'}
        </Button>
        {conflict && (
          <Button
            type="button"
            disabled={pending}
            onClick={() => void reload()}
          >
            Reload Avatar
          </Button>
        )}
        <Button
          type="button"
          disabled={pending}
          onClick={() => {
            setSelection(null);
            setError('');
          }}
        >
          Cancel
        </Button>
      </div>
    </form>
  ) : (
    <div className="camp-account-avatar">
      <Button
        ref={opener}
        onClick={() => {
          restoreFocus.current = true;
          setSelection({ avatar: user.avatar, expected: user.avatar });
          setError('');
          setConflict(false);
          setNotice('');
        }}
      >
        Change Avatar…
      </Button>
      {notice && <p role="status">{notice}</p>}
    </div>
  );
}
