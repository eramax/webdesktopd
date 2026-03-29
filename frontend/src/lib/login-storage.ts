const DB_NAME = 'webdesktopd-login-storage';
const DB_VERSION = 1;
const SECRET_STORE = 'remembered-logins';
const SECRET_ID = 'current';

export type AuthMode = 'password' | 'privateKey';

export interface RememberedLoginPayload {
  username: string;
  authMode: AuthMode;
  secret: string;
  keyLabel?: string;
}

interface StoredLoginRecord extends RememberedLoginPayload {
  id: string;
  version: 1;
}

function hasIndexedDB(): boolean {
  return typeof indexedDB !== 'undefined';
}

function openDatabase(): Promise<IDBDatabase | null> {
  if (!hasIndexedDB()) return Promise.resolve(null);

  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);

    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(SECRET_STORE)) {
        db.createObjectStore(SECRET_STORE, { keyPath: 'id' });
      }
    };

    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function idbGet<T>(db: IDBDatabase, storeName: string, id: string): Promise<T | undefined> {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(storeName, 'readonly');
    const store = tx.objectStore(storeName);
    const request = store.get(id);
    request.onsuccess = () => resolve(request.result as T | undefined);
    request.onerror = () => reject(request.error);
  });
}

function idbPut(db: IDBDatabase, storeName: string, value: unknown): Promise<void> {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(storeName, 'readwrite');
    const store = tx.objectStore(storeName);
    const request = store.put(value);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error);
  });
}

function idbDelete(db: IDBDatabase, storeName: string, id: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(storeName, 'readwrite');
    const store = tx.objectStore(storeName);
    const request = store.delete(id);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error);
  });
}

export function encodeRememberedLogin(payload: RememberedLoginPayload): StoredLoginRecord {
  return {
    id: SECRET_ID,
    version: 1,
    username: payload.username,
    authMode: payload.authMode,
    secret: payload.secret,
    keyLabel: payload.keyLabel
  };
}

export function decodeRememberedLogin(record: unknown): RememberedLoginPayload | null {
  if (!record || typeof record !== 'object') return null;

  const candidate = record as Partial<StoredLoginRecord> & Record<string, unknown>;
  if (candidate.version !== 1 || candidate.id !== SECRET_ID) return null;
  if (candidate.authMode !== 'password' && candidate.authMode !== 'privateKey') return null;
  if (typeof candidate.username !== 'string' || typeof candidate.secret !== 'string') return null;
  if (candidate.keyLabel !== undefined && typeof candidate.keyLabel !== 'string') return null;

  return {
    username: candidate.username,
    authMode: candidate.authMode,
    secret: candidate.secret,
    keyLabel: candidate.keyLabel
  };
}

export async function loadRememberedLogin(): Promise<RememberedLoginPayload | null> {
  const db = await openDatabase().catch(() => null);
  if (!db) return null;

  const record = await idbGet<StoredLoginRecord>(db, SECRET_STORE, SECRET_ID).catch(() => undefined);
  const decoded = decodeRememberedLogin(record);

  if (decoded) return decoded;

  if (record) {
    await idbDelete(db, SECRET_STORE, SECRET_ID).catch(() => undefined);
  }

  return null;
}

export async function saveRememberedLogin(payload: RememberedLoginPayload): Promise<void> {
  const db = await openDatabase().catch(() => null);
  if (!db) return;

  await idbPut(db, SECRET_STORE, encodeRememberedLogin(payload)).catch(() => undefined);
}

export async function clearRememberedLogin(): Promise<void> {
  const db = await openDatabase().catch(() => null);
  if (!db) return;

  await idbDelete(db, SECRET_STORE, SECRET_ID).catch(() => undefined);
}
