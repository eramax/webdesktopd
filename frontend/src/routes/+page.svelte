<script lang="ts">
  import { goto } from '$app/navigation';
  import { session } from '$lib/session.svelte';

  let username = $state('');
  let password = $state('');
  let privateKey = $state('');
  let showPrivateKey = $state(false);
  let loading = $state(false);
  let error = $state<string | null>(null);

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    loading = true;

    try {
      const body: Record<string, string> = { username, password };
      if (privateKey.trim()) {
        body.privateKeyPem = privateKey.trim();
      }

      const res = await fetch('/auth', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });

      if (!res.ok) {
        const text = await res.text().catch(() => '');
        if (res.status === 401) {
          error = 'Invalid credentials. Please check your username and password.';
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

      session.login(username, data.token);
      await goto('/desktop');
    } catch (err) {
      error = err instanceof Error ? err.message : 'Network error. Is the server running?';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head>
  <title>webdesktopd – Login</title>
</svelte:head>

<div class="min-h-screen bg-zinc-950 flex items-center justify-center p-4">
  <div class="w-full max-w-md">
    <!-- Card -->
    <div class="bg-zinc-900 border border-zinc-800 rounded-xl shadow-2xl p-8">
      <!-- Title -->
      <div class="mb-8 text-center">
        <h1 class="text-2xl font-bold text-zinc-100 tracking-tight">webdesktopd</h1>
        <p class="mt-1 text-sm text-zinc-500">Sign in with your system credentials</p>
      </div>

      <!-- Error banner -->
      {#if error}
        <div class="mb-5 rounded-lg bg-red-950 border border-red-800 px-4 py-3 text-sm text-red-300">
          {error}
        </div>
      {/if}

      <form onsubmit={handleSubmit} class="space-y-5">
        <!-- Username -->
        <div>
          <label for="username" class="block text-sm font-medium text-zinc-400 mb-1.5">
            Username
          </label>
          <input
            id="username"
            type="text"
            autocomplete="username"
            required
            bind:value={username}
            disabled={loading}
            class="w-full rounded-lg bg-zinc-800 border border-zinc-700 px-4 py-2.5 text-zinc-100 placeholder-zinc-600 text-sm focus:outline-none focus:ring-2 focus:ring-blue-600 focus:border-transparent disabled:opacity-50 disabled:cursor-not-allowed transition"
            placeholder="e.g. alice"
          />
        </div>

        <!-- Password -->
        <div>
          <label for="password" class="block text-sm font-medium text-zinc-400 mb-1.5">
            Password
          </label>
          <input
            id="password"
            type="password"
            autocomplete="current-password"
            bind:value={password}
            disabled={loading}
            class="w-full rounded-lg bg-zinc-800 border border-zinc-700 px-4 py-2.5 text-zinc-100 placeholder-zinc-600 text-sm focus:outline-none focus:ring-2 focus:ring-blue-600 focus:border-transparent disabled:opacity-50 disabled:cursor-not-allowed transition"
            placeholder="••••••••"
          />
        </div>

        <!-- Private key collapsible -->
        <div>
          <button
            type="button"
            onclick={() => (showPrivateKey = !showPrivateKey)}
            class="flex items-center gap-2 text-sm text-zinc-500 hover:text-zinc-300 transition select-none"
          >
            <svg
              class="w-4 h-4 transition-transform {showPrivateKey ? 'rotate-90' : ''}"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              viewBox="0 0 24 24"
            >
              <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
            </svg>
            Private key (optional)
          </button>

          {#if showPrivateKey}
            <div class="mt-2">
              <textarea
                id="privateKey"
                bind:value={privateKey}
                disabled={loading}
                rows="6"
                class="w-full rounded-lg bg-zinc-800 border border-zinc-700 px-4 py-2.5 text-zinc-100 placeholder-zinc-600 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-blue-600 focus:border-transparent disabled:opacity-50 disabled:cursor-not-allowed resize-y transition"
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
              ></textarea>
              <p class="mt-1 text-xs text-zinc-600">
                Paste your PEM-encoded SSH private key. Leave empty to use password only.
              </p>
            </div>
          {/if}
        </div>

        <!-- Submit -->
        <button
          type="submit"
          disabled={loading || !username}
          class="w-full rounded-lg bg-blue-600 hover:bg-blue-500 disabled:bg-blue-900 disabled:cursor-not-allowed text-white font-semibold py-2.5 text-sm transition focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 focus:ring-offset-zinc-900"
        >
          {#if loading}
            <span class="flex items-center justify-center gap-2">
              <svg class="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
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
