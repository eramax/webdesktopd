const DB_NAME = 'webdesktopd-login-storage';
const DB_VERSION = 1;
const KEY_STORE = 'crypto-keys';
const SECRET_STORE = 'remembered-logins';
const KEY_ID = 'default';
const SECRET_ID = 'current';

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export type AuthMode = 'password' | 'privateKey';

export interface RememberedLoginPayload {
  username: string;
  authMode: AuthMode;
  secret: string;
  keyLabel?: string;
}

export interface EncryptedLoginPayload {
  version: 1;
  iv: string;
  ciphertext: string;
}

let cachedKeyPromise: Promise<CryptoKey> | null = null;

function hasIndexedDB(): boolean {
  return typeof indexedDB !== 'undefined';
}

function base64Encode(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

function base64Decode(value: string): ArrayBuffer {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

function openDatabase(): Promise<IDBDatabase | null> {
  if (!hasIndexedDB()) return Promise.resolve(null);

  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);

    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(KEY_STORE)) {
        db.createObjectStore(KEY_STORE, { keyPath: 'id' });
      }
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

async function createCryptoKey(): Promise<CryptoKey> {
  return crypto.subtle.generateKey(
    {
      name: 'AES-GCM',
      length: 256
    },
    false,
    ['encrypt', 'decrypt']
  );
}

async function loadStoredCryptoKey(db: IDBDatabase): Promise<CryptoKey | null> {
  const stored = await idbGet<{ id: string; key: CryptoKey }>(db, KEY_STORE, KEY_ID);
  return stored?.key ?? null;
}

async function persistCryptoKey(db: IDBDatabase, key: CryptoKey): Promise<void> {
  await idbPut(db, KEY_STORE, { id: KEY_ID, key });
}

async function getCryptoKey(): Promise<CryptoKey> {
  if (cachedKeyPromise) return cachedKeyPromise;

  cachedKeyPromise = (async () => {
    const db = await openDatabase().catch(() => null);
    if (!db) {
      return createCryptoKey();
    }

    const storedKey = await loadStoredCryptoKey(db).catch(() => null);
    if (storedKey) {
      return storedKey;
    }

    const key = await createCryptoKey();
    await persistCryptoKey(db, key).catch(() => undefined);
    return key;
  })();

  return cachedKeyPromise;
}

async function encryptWithKey(payload: RememberedLoginPayload, key: CryptoKey): Promise<EncryptedLoginPayload> {
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const plaintext = encoder.encode(JSON.stringify(payload));
  const ciphertext = await crypto.subtle.encrypt(
    {
      name: 'AES-GCM',
      iv
    },
    key,
    plaintext
  );

  return {
    version: 1,
    iv: base64Encode(iv),
    ciphertext: base64Encode(new Uint8Array(ciphertext))
  };
}

async function decryptWithKey(record: EncryptedLoginPayload, key: CryptoKey): Promise<RememberedLoginPayload> {
  const plaintext = await crypto.subtle.decrypt(
    {
      name: 'AES-GCM',
      iv: base64Decode(record.iv)
    },
    key,
    base64Decode(record.ciphertext)
  );

  return JSON.parse(decoder.decode(plaintext)) as RememberedLoginPayload;
}

export async function encryptSecretPayload(payload: RememberedLoginPayload): Promise<EncryptedLoginPayload> {
  const key = await getCryptoKey();
  return encryptWithKey(payload, key);
}

export async function decryptSecretPayload(record: EncryptedLoginPayload): Promise<RememberedLoginPayload> {
  const key = await getCryptoKey();
  return decryptWithKey(record, key);
}

export async function loadRememberedLogin(): Promise<RememberedLoginPayload | null> {
  const db = await openDatabase().catch(() => null);
  if (!db) return null;

  const record = await idbGet<EncryptedLoginPayload & { id: string }>(db, SECRET_STORE, SECRET_ID).catch(() => undefined);
  if (!record) return null;

  try {
    return await decryptSecretPayload({
      version: record.version,
      iv: record.iv,
      ciphertext: record.ciphertext
    });
  } catch {
    return null;
  }
}

export async function saveRememberedLogin(payload: RememberedLoginPayload): Promise<void> {
  const db = await openDatabase().catch(() => null);
  if (!db) return;

  const encrypted = await encryptSecretPayload(payload);
  await idbPut(db, SECRET_STORE, {
    id: SECRET_ID,
    ...encrypted
  }).catch(() => undefined);
}

export async function clearRememberedLogin(): Promise<void> {
  const db = await openDatabase().catch(() => null);
  if (!db) return;

  await idbDelete(db, SECRET_STORE, SECRET_ID).catch(() => undefined);
}
