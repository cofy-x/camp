import { useRef, useState } from 'react';
import { APIError } from '../../shared/api/http';
import {
  clearSessionReceipt,
  readSessionReceipt,
  receiptKey,
  writeSessionReceipt,
} from '../../shared/sessionReceipt';
import { workspaceApi, type Room } from './api';

export type RoomNameEdit = {
  target?: Pick<Room, 'id' | 'name' | 'updatedAt'>;
  name: string;
};

type RoomNameRequest = RoomNameEdit & { requestId: string };

function isRoomNameRequest(value: unknown): value is RoomNameRequest {
  if (!value || typeof value !== 'object') return false;
  const request = value as Partial<RoomNameRequest>;
  return (
    typeof request.requestId === 'string' &&
    typeof request.name === 'string' &&
    (request.target === undefined ||
      (typeof request.target?.id === 'string' &&
        typeof request.target.name === 'string' &&
        typeof request.target.updatedAt === 'string'))
  );
}

export function useRoomNameOperation(userId: string, refresh: () => void) {
  const key = receiptKey(userId, 'room-name', 'workspace');
  const [pending, setPending] = useState(() =>
    readSessionReceipt(key, isRoomNameRequest),
  );
  const [edit, setEdit] = useState<RoomNameEdit>();
  const [confirmed, setConfirmed] = useState<Room>();
  const [busy, setBusy] = useState(false);
  const [missing, setMissing] = useState(false);
  const [error, setError] = useState('');
  const inFlight = useRef(false);

  function retire(request: RoomNameRequest) {
    if (
      readSessionReceipt(key, isRoomNameRequest)?.requestId ===
      request.requestId
    )
      clearSessionReceipt(key);
    setPending(null);
  }

  async function resolve(
    request: RoomNameRequest,
    mode: 'submit' | 'check' | 'retry',
  ) {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError('');
    setMissing(false);
    const { name, target, requestId } = request;
    try {
      const { room } =
        mode === 'check'
          ? await workspaceApi.roomNameReceipt(requestId)
          : target
            ? await workspaceApi.renameRoom(
                target.id,
                name,
                target.updatedAt,
                requestId,
              )
            : await workspaceApi.createRoom(name, requestId);
      if (!room?.id || room.name !== name || (target && room.id !== target.id))
        throw new Error(
          'The saved receipt does not match this Room name request.',
        );
      retire(request);
      if (mode !== 'submit') setConfirmed(room);
      refresh();
      return room;
    } catch (reason) {
      if (reason instanceof APIError) {
        if (
          mode === 'check' &&
          reason.status === 404 &&
          reason.code === 'not_found'
        ) {
          setMissing(true);
          setError(
            'No saved receipt found. You can retry the original request.',
          );
          return;
        }
        if (
          mode !== 'check' &&
          reason.status >= 400 &&
          reason.status < 500 &&
          reason.code &&
          reason.code !== 'http_error' &&
          ![408, 429].includes(reason.status) &&
          (mode === 'submit' ||
            (![401, 403].includes(reason.status) &&
              reason.code !== 'request_conflict'))
        ) {
          retire(request);
          setEdit({ name, target });
          if (reason.status === 409) refresh();
        }
      }
      setError(
        reason instanceof Error
          ? reason.message
          : 'Could not confirm the Room name request.',
      );
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  async function submit(input: RoomNameEdit) {
    if (pending || inFlight.current) return;
    const request = { ...input, requestId: crypto.randomUUID() };
    try {
      writeSessionReceipt(key, request);
    } catch (reason) {
      setEdit(input);
      setError(reason instanceof Error ? reason.message : String(reason));
      return;
    }
    setEdit(undefined);
    setConfirmed(undefined);
    setPending(request);
    return resolve(request, 'submit');
  }

  return {
    pending,
    edit,
    confirmed,
    busy,
    missing,
    error,
    submit,
    check: () => pending && resolve(pending, 'check'),
    retry: () => pending && missing && resolve(pending, 'retry'),
    discardEdit() {
      setEdit(undefined);
      setError('');
    },
    dismissConfirmation: () => setConfirmed(undefined),
  };
}
