<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    clearRememberedLogin,
    loadRememberedLogin,
    saveRememberedLogin
  } from '$lib/login-storage';
  import { session } from '$lib/session.svelte';

  type AuthMode = 'password' | 'privateKey';

  let username = $state('');
  let password = $state('');
  let privateKey = $state('');
  let privateKeyLabel = $state<string | null>(null);
  let authMode = $state<AuthMode>('password');
  let rememberMe = $state(false);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let keyInput = $state<HTMLInputElement | null>(null);

  onMount(() => {
    void (async () => {
      const saved = await loadRememberedLogin();
      if (!saved) return;

      username = saved.username;
      authMode = saved.authMode;
      rememberMe = true;

      if (saved.authMode === 'password') {
        password = saved.secret;
        privateKey = '';
        privateKeyLabel = null;
      } else {
        privateKey = saved.secret;
        privateKeyLabel = saved.keyLabel ?? 'Remembered private key';
        password = '';
      }
    })();
  });

  async function handlePrivateKeyFile(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];

    if (!file) return;

    try {
      privateKey = await file.text();
      privateKeyLabel = file.name;
      authMode = 'privateKey';
      error = null;
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to read private key file.';
    }
  }

  function clearPrivateKeySelection() {
    privateKey = '';
    privateKeyLabel = null;
    if (keyInput) {
      keyInput.value = '';
    }
  }

  async function forgetThisBrowser() {
    await clearRememberedLogin();
    username = '';
    password = '';
    privateKey = '';
    privateKeyLabel = null;
    authMode = 'password';
    rememberMe = false;
    error = null;
    if (keyInput) {
      keyInput.value = '';
    }
  }

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    loading = true;

    try {
      const trimmedUsername = username.trim();
      const trimmedPassword = password.trim();
      const trimmedPrivateKey = privateKey.trim();

      const body: Record<string, string> = { username: trimmedUsername };
      const secret = authMode === 'privateKey' ? trimmedPrivateKey : trimmedPassword;

      if (!secret) {
        error = authMode === 'privateKey' ? 'Choose a private key file or paste a key.' : 'Enter your password.';
        return;
      }

      if (authMode === 'privateKey') {
        body.privateKeyPem = secret;
      } else {
        body.password = secret;
      }

      const res = await fetch('/auth', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });

      if (!res.ok) {
        const text = await res.text().catch(() => '');
        if (res.status === 401) {
          error = 'Invalid credentials. Please check your username and secret.';
        } else {
          error = `Authentication failed (${res.status})${text ? ': ' + text : ''}.`;
        }
        return;
      }

      const data = (await res.json()) as { token: string };
      if (!data.token) {
        error = 'Server returned an invalid response (missing token).';
        return;
      }

      const rememberPayload = {
        username: trimmedUsername,
        authMode,
        secret,
        keyLabel: authMode === 'privateKey' ? privateKeyLabel ?? undefined : undefined
      };

      if (rememberMe) {
        await saveRememberedLogin(rememberPayload);
      } else {
        await clearRememberedLogin();
      }

      session.login(trimmedUsername, data.token);
      await goto('/desktop');
    } catch (err) {
      error = err instanceof Error ? err.message : 'Network error. Is the server running?';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head>
  <title>webdesktopd - Login</title>
</svelte:head>

<div class="min-h-screen bg-[radial-gradient(circle_at_top,_rgba(37,99,235,0.24),_transparent_42%),linear-gradient(180deg,_#09090b_0%,_#111827_100%)] flex items-center justify-center p-4">
  <div class="w-full max-w-md">
    <div class="rounded-2xl border border-white/10 bg-zinc-950/90 p-8 shadow-2xl shadow-black/40 backdrop-blur">
      <div class="mb-8 text-center">
        <div class="mx-auto mb-4 h-12 w-12 rounded-2xl border border-blue-500/30 bg-blue-500/10 text-blue-300 flex items-center justify-center text-lg font-semibold">
          wd
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-zinc-50">webdesktopd</h1>
        <p class="mt-1 text-sm text-zinc-400">Sign in with password or an SSH private key</p>
      </div>

      {#if error}
        <div class="mb-5 rounded-lg border border-red-900/70 bg-red-950/80 px-4 py-3 text-sm text-red-200">
          {error}
        </div>
      {/if}

      <form onsubmit={handleSubmit} class="space-y-5">
        <div>
          <label for="username" class="mb-1.5 block text-sm font-medium text-zinc-300">Username</label>
          <input
            id="username"
            type="text"
            autocomplete="username"
            required
            bind:value={username}
            disabled={loading}
            class="w-full rounded-lg border border-zinc-800 bg-zinc-900 px-4 py-2.5 text-sm text-zinc-100 placeholder-zinc-600 transition focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50 disabled:cursor-not-allowed disabled:opacity-50"
            placeholder="e.g. alice"
          />
        </div>

        <div>
          <div class="mb-2 flex rounded-xl border border-zinc-800 bg-zinc-900 p-1">
            <button
              type="button"
              onclick={() => (authMode = 'password')}
              aria-pressed={authMode === 'password'}
              class="flex-1 rounded-lg px-3 py-2 text-sm font-medium transition {authMode === 'password' ? 'bg-blue-600 text-white shadow' : 'text-zinc-400 hover:text-zinc-100'}"
            >
              Password
            </button>
            <button
              type="button"
              onclick={() => (authMode = 'privateKey')}
              aria-pressed={authMode === 'privateKey'}
              class="flex-1 rounded-lg px-3 py-2 text-sm font-medium transition {authMode === 'privateKey' ? 'bg-blue-600 text-white shadow' : 'text-zinc-400 hover:text-zinc-100'}"
            >
              Private key
            </button>
          </div>

          {#if authMode === 'password'}
            <div>
              <label for="password" class="mb-1.5 block text-sm font-medium text-zinc-300">Password</label>
              <input
                id="password"
                type="password"
                autocomplete="current-password"
                bind:value={password}
                disabled={loading}
                class="w-full rounded-lg border border-zinc-800 bg-zinc-900 px-4 py-2.5 text-sm text-zinc-100 placeholder-zinc-600 transition focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50 disabled:cursor-not-allowed disabled:opacity-50"
                placeholder="••••••••"
              />
            </div>
          {:else}
            <div class="space-y-3">
              <div>
                <label for="privateKeyFile" class="mb-1.5 block text-sm font-medium text-zinc-300">
                  Private key file
                </label>
                <input
                  bind:this={keyInput}
                  id="privateKeyFile"
                  type="file"
                  accept=".pem,.key,.txt,.pub"
                  onchange={handlePrivateKeyFile}
                  disabled={loading}
                  class="w-full cursor-pointer rounded-lg border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm text-zinc-300 file:mr-4 file:rounded-md file:border-0 file:bg-blue-600 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-white hover:file:bg-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50 disabled:cursor-not-allowed disabled:opacity-50"
                />
              </div>

              {#if privateKeyLabel}
                <div class="flex items-center justify-between rounded-lg border border-zinc-800 bg-zinc-900 px-4 py-2 text-xs text-zinc-400">
                  <span class="truncate">Loaded: {privateKeyLabel}</span>
                  <button
                    type="button"
                    onclick={clearPrivateKeySelection}
                    disabled={loading}
                    class="ml-4 text-zinc-500 transition hover:text-zinc-200 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    Clear
                  </button>
                </div>
              {/if}

              <div>
                <label for="privateKey" class="mb-1.5 block text-sm font-medium text-zinc-300">
                  Private key contents
                </label>
                <textarea
                  id="privateKey"
                  bind:value={privateKey}
                  disabled={loading}
                  rows="7"
                  class="w-full resize-y rounded-lg border border-zinc-800 bg-zinc-900 px-4 py-2.5 font-mono text-xs text-zinc-100 placeholder-zinc-600 transition focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50 disabled:cursor-not-allowed disabled:opacity-50"
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
                ></textarea>
                <p class="mt-1 text-xs text-zinc-500">
                  You can pick a file or paste the key here. If remember me is enabled, the selected key is stored in this browser's IndexedDB.
                </p>
              </div>
            </div>
          {/if}
        </div>

        <label class="flex items-center gap-3 rounded-lg border border-zinc-800 bg-zinc-900 px-4 py-3 text-sm text-zinc-300">
          <input
            type="checkbox"
            bind:checked={rememberMe}
            disabled={loading}
            class="h-4 w-4 rounded border-zinc-700 bg-zinc-950 text-blue-600 focus:ring-blue-500"
          />
          <span>Remember me on this browser</span>
        </label>

        <button
          type="button"
          onclick={forgetThisBrowser}
          disabled={loading}
          class="w-full rounded-lg border border-zinc-800 bg-zinc-900 px-4 py-2.5 text-sm font-medium text-zinc-400 transition hover:border-zinc-700 hover:text-zinc-100 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Forget this browser
        </button>

        <button
          type="submit"
          disabled={loading || !username.trim() || (authMode === 'password' ? !password.trim() : !privateKey.trim())}
          class="w-full rounded-lg bg-blue-600 py-2.5 text-sm font-semibold text-white transition hover:bg-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 focus:ring-offset-zinc-950 disabled:cursor-not-allowed disabled:bg-blue-900"
        >
          {#if loading}
            <span class="flex items-center justify-center gap-2">
              <svg class="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"></path>
              </svg>
              Signing in...
            </span>
          {:else}
            Sign in
          {/if}
        </button>
      </form>
    </div>
  </div>
</div>
