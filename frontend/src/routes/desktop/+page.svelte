<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { session } from '$lib/session.svelte';
  import { FrameType } from '$lib/protocol';
  import type { Frame } from '$lib/protocol';
  import Terminal from '$lib/components/Terminal.svelte';
  import StatsDock from '$lib/components/StatsDock.svelte';

  // Redirect to login if not authenticated
  onMount(() => {
    if (!session.token || !session.client) {
      goto('/');
      return;
    }

    // Connect WebSocket
    session.client.connect();

    // Handle session sync from server: restore existing PTY channels.
    // Use registerBroadcast so the stats dock handler on channel 0 is not displaced.
    session.client.registerBroadcast('session-sync', (frame: Frame) => {
      if (frame.type === FrameType.SessionSync) {
        try {
          const state = JSON.parse(new TextDecoder().decode(frame.payload)) as {
            ptyChannels?: Array<{ chanID: number; username?: string }>;
          };
          if (state.ptyChannels) {
            for (const ch of state.ptyChannels) {
              session.addPTYChannel(ch.chanID, `Terminal ${ch.chanID}`);
            }
            if (session.ptyChannels.length > 0 && session.activeChannel === null) {
              session.setActiveChannel(session.ptyChannels[0].chanID);
            }
          }
        } catch {
          // ignore malformed sync frames
        }
      }
    });

    // If no channels exist after a short delay (fresh session), open channel 1
    const timer = setTimeout(() => {
      if (session.ptyChannels.length === 0) {
        openNewTerminal();
      }
    }, 500);

    return () => {
      clearTimeout(timer);
      session.client?.unregisterBroadcast('session-sync');
    };
  });

  function openNewTerminal() {
    if (!session.client) return;
    const chanID = session.nextChannelID();
    session.addPTYChannel(chanID, `Terminal ${chanID}`);
    session.setActiveChannel(chanID);
    // openPTY is sent by Terminal.svelte after it registers its handler —
    // do NOT call it here to avoid a race where output arrives before the handler.
  }

  function closeTerminal(chanID: number) {
    if (!session.client) return;
    // ClosePTY is a control-plane message: always sent on chanID 0.
    session.client.sendJSON(FrameType.ClosePTY, 0, { channel: chanID });
    session.client.unregister(chanID);
    session.removePTYChannel(chanID);
  }

  function handleDisconnect() {
    session.logout();
    goto('/');
  }

  // Derived: is the connection live?
  let connected = $derived(session.connected);
</script>

<svelte:head>
  <title>webdesktopd – Desktop</title>
</svelte:head>

<div class="flex flex-col h-screen bg-zinc-950 text-zinc-100 overflow-hidden">
  <!-- Top bar -->
  <header class="flex items-center justify-between px-4 py-2 bg-zinc-900 border-b border-zinc-800 shrink-0">
    <div class="flex items-center gap-3">
      <span class="font-semibold text-zinc-200 text-sm">webdesktopd</span>
      <!-- Connection indicator -->
      <span class="flex items-center gap-1.5 text-xs {connected ? 'text-green-400' : 'text-red-400'}">
        <span class="w-2 h-2 rounded-full {connected ? 'bg-green-400' : 'bg-red-400'}"></span>
        {connected ? 'Connected' : 'Disconnected'}
      </span>
    </div>

    <div class="flex items-center gap-3">
      {#if session.username}
        <span class="text-sm text-zinc-400">{session.username}</span>
      {/if}
      <button
        onclick={handleDisconnect}
        class="text-xs px-3 py-1.5 rounded-md bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 text-zinc-300 hover:text-zinc-100 transition"
      >
        Disconnect
      </button>
    </div>
  </header>

  <!-- Main content: sidebar + terminal area -->
  <div class="flex flex-1 overflow-hidden">
    <!-- Left sidebar: terminal tabs -->
    <aside class="flex flex-col w-48 bg-zinc-900 border-r border-zinc-800 shrink-0 overflow-y-auto">
      <div class="flex items-center justify-between px-3 py-2 border-b border-zinc-800">
        <span class="text-xs font-medium text-zinc-500 uppercase tracking-wider">Terminals</span>
        <button
          onclick={openNewTerminal}
          title="Open new terminal"
          class="w-6 h-6 flex items-center justify-center rounded text-zinc-400 hover:text-zinc-100 hover:bg-zinc-700 transition text-lg leading-none"
        >
          +
        </button>
      </div>

      <nav class="flex flex-col py-1 gap-0.5 px-1">
        {#each session.ptyChannels as ch (ch.chanID)}
          <div
            class="group flex items-center rounded-md overflow-hidden {session.activeChannel === ch.chanID
              ? 'bg-blue-700 text-white'
              : 'text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200'}"
          >
            <button
              onclick={() => session.setActiveChannel(ch.chanID)}
              class="flex-1 text-left px-3 py-2 text-sm truncate"
            >
              <span class="flex items-center gap-2">
                <!-- Terminal icon -->
                <svg class="w-3.5 h-3.5 shrink-0 opacity-70" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                  <polyline points="4 17 10 11 4 5" />
                  <line x1="12" y1="19" x2="20" y2="19" />
                </svg>
                {ch.label}
              </span>
            </button>

            <!-- Close tab button -->
            <button
              onclick={() => closeTerminal(ch.chanID)}
              title="Close terminal"
              class="opacity-0 group-hover:opacity-100 px-1.5 py-1 text-zinc-500 hover:text-red-400 transition shrink-0"
            >
              <svg class="w-3 h-3" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
        {/each}

        {#if session.ptyChannels.length === 0}
          <p class="text-xs text-zinc-600 px-3 py-4 text-center">No terminals open</p>
        {/if}
      </nav>
    </aside>

    <!-- Terminal / main area -->
    <main class="flex-1 flex flex-col overflow-hidden bg-black">
      {#if session.client && session.activeChannel !== null}
        {#each session.ptyChannels as ch (ch.chanID)}
          <div
            class="flex-1 overflow-hidden"
            style="display: {session.activeChannel === ch.chanID ? 'flex' : 'none'}; flex-direction: column;"
          >
            <Terminal chanID={ch.chanID} client={session.client} connectCount={session.connectCount} />
          </div>
        {/each}
      {:else}
        <div class="flex-1 flex items-center justify-center text-zinc-700 text-sm">
          {#if !session.client}
            Not connected
          {:else}
            No terminal selected
          {/if}
        </div>
      {/if}
    </main>
  </div>

  <!-- Stats dock at the bottom -->
  {#if session.client}
    <StatsDock client={session.client} />
  {/if}
</div>
