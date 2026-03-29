import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeRememberedLogin, encodeRememberedLogin } from '../frontend/src/lib/login-storage';

describe('login storage', () => {
  test('stores and restores the remembered payload without transformation', () => {
    const payload = {
      username: 'alice',
      authMode: 'privateKey' as const,
      secret: '-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----',
      keyLabel: 'id_ed25519'
    };

    const record = encodeRememberedLogin(payload);

    assert.equal(record.version, 1);
    assert.equal(record.id, 'current');

    const restored = decodeRememberedLogin(record);
    assert.deepEqual(restored, payload);
  });

  test('rejects incompatible stored records', () => {
    assert.equal(
      decodeRememberedLogin({
        id: 'current',
        version: 1,
        username: 'alice',
        authMode: 'privateKey',
        ciphertext: 'not-supported-anymore'
      }),
      null
    );
  });
});
