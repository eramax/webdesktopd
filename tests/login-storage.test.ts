import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { decryptSecretPayload, encryptSecretPayload } from '../frontend/src/lib/login-storage';

describe('login storage crypto', () => {
  test('encrypts and decrypts the secret payload without losing data', async () => {
    const payload = {
      username: 'alice',
      authMode: 'privateKey' as const,
      secret: '-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----',
      keyLabel: 'id_ed25519'
    };

    const record = await encryptSecretPayload(payload);

    assert.ok(!record.ciphertext.includes(payload.secret));
    assert.equal(record.version, 1);

    const restored = await decryptSecretPayload(record);
    assert.deepEqual(restored, payload);
  });
});
