<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { session } from '$lib/session.svelte';
  import { FrameType } from '$lib/protocol';
  import type { Frame } from '$lib/protocol';
  import Terminal from '$lib/components/Terminal.svelte';
  import Dock from '$lib/components/Dock.svelte';

  // Redirect to login if not authenticated
  onMount(() => {
    if (!session.token || !session.client) {
      goto('/');
      return;
    }

    // Connect WebSocket
    session.client.connect();

    // Handle session sync from server: restore existing PTY channels.
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

    // Fresh session: open first terminal after a short delay
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
  }

  function closeTerminal(chanID: number) {
    if (!session.client) return;
    session.client.sendJSON(FrameType.ClosePTY, 0, { channel: chanID });
    session.client.unregister(chanID);
    session.removePTYChannel(chanID);
  }

  let connected = $derived(session.connected);
</script>

<svelte:head>
  <title>webdesktopd</title>
</svelte:head>

<!-- Full-screen desktop -->
<div class="flex flex-col h-screen bg-zinc-950 text-zinc-100 overflow-hidden">

  <!-- Thin top bar: title + connection status + user + disconnect -->
  <header class="flex items-center justify-between px-4 py-1.5 bg-zinc-900 border-b border-zinc-800 shrink-0">
    <span class="font-semibold text-zinc-200 text-sm tracking-tight">webdesktopd</span>

    <div class="flex items-center gap-3">
      <span class="flex items-center gap-1.5 text-xs {connected ? 'text-green-400' : 'text-red-400'}">
        <span class="w-1.5 h-1.5 rounded-full {connected ? 'bg-green-400' : 'bg-red-400'}"></span>
        {connected ? 'Connected' : 'Reconnecting…'}
      </span>

      {#if session.username}
        <span class="text-xs text-zinc-500">{session.username}</span>
      {/if}

      <button
        onclick={() => { session.logout(); goto('/'); }}
        class="text-xs px-2.5 py-1 rounded bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 text-zinc-400 hover:text-zinc-100 transition"
      >
        Disconnect
      </button>
    </div>
  </header>

  <!-- Desktop / app area -->
  <!-- position:relative so absolute children fill exactly this box -->
  <main class="flex-1 overflow-hidden bg-black relative">
    {#if session.client}
      {#each session.ptyChannels as ch (ch.chanID)}
        <!--
          All terminals are always position:absolute and fill the same box.
          Only visibility changes on switch — no layout change → no resize
          event → no SIGWINCH → btop/vim won't flash on tab switch.
        -->
        <div
          class="absolute inset-0"
          style="visibility: {session.activeChannel === ch.chanID ? 'visible' : 'hidden'}"
        >
          <Terminal chanID={ch.chanID} client={session.client} connectCount={session.connectCount} />
        </div>
      {/each}

      {#if session.ptyChannels.length === 0}
        <div class="flex h-full items-center justify-center text-zinc-700 text-sm">
          No app open — click the terminal icon in the dock
        </div>
      {/if}
    {/if}
  </main>

  <!-- Dock -->
  {#if session.client}
    <Dock
      client={session.client}
      channels={session.ptyChannels}
      activeChannel={session.activeChannel}
      onNewTerminal={openNewTerminal}
      onSelectChannel={(id) => session.setActiveChannel(id)}
      onCloseChannel={closeTerminal}
    />
  {/if}
</div>
