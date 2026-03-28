<script lang="ts">
  import type { WSClient } from '$lib/client';
  import type { PTYChannel } from '$lib/session.svelte';
  import Terminal from './Terminal.svelte';

  interface Props {
    client: WSClient;
    channels: PTYChannel[];
    activeChannel: number | null;
    connectCount: number;
    pinnedChannels: number[];
    onNewTerminal: () => void;
    onSelectChannel: (chanID: number) => void;
    onCloseChannel: (chanID: number) => void;
    onRenameChannel: (chanID: number, label: string) => void;
    onPinChannel: (chanID: number) => void;
    onUnpinChannel: (chanID: number) => void;
  }

  let {
    client, channels, activeChannel, connectCount, pinnedChannels,
    onNewTerminal, onSelectChannel, onCloseChannel, onRenameChannel, onPinChannel, onUnpinChannel,
  }: Props = $props();

  let renamingChanID = $state<number | null>(null);
  let renameValue = $state('');

  function startRename(chanID: number, label: string) {
    renamingChanID = chanID;
    renameValue = label;
  }

  function commitRename() {
    if (renamingChanID !== null && renameValue.trim()) {
      onRenameChannel(renamingChanID, renameValue.trim());
    }
    renamingChanID = null;
  }

  let pinned = $derived(channels.filter(ch => pinnedChannels.includes(ch.chanID)));
  let unpinned = $derived(channels.filter(ch => !pinnedChannels.includes(ch.chanID)));
</script>

<div class="flex h-full">
  <!-- ── Left sidebar ── -->
  <aside class="w-44 shrink-0 flex flex-col bg-zinc-900 border-r border-zinc-800 select-none">

    {#if pinned.length > 0}
      <div class="px-3 pt-2.5 pb-1">
        <span class="text-[10px] uppercase tracking-wider text-zinc-600 font-semibold">Pinned</span>
      </div>
      {#each pinned as ch (ch.chanID)}
        {@render tabItem(ch, true)}
      {/each}
      <div class="mx-2 my-1.5 border-t border-zinc-800"></div>
    {/if}

    <div class="flex-1 overflow-y-auto py-1">
      {#if unpinned.length > 0}
        {#if pinned.length > 0}
          <div class="px-3 pb-1">
            <span class="text-[10px] uppercase tracking-wider text-zinc-600 font-semibold">Terminals</span>
          </div>
        {/if}
        {#each unpinned as ch (ch.chanID)}
          {@render tabItem(ch, false)}
        {/each}
      {:else if pinned.length === 0}
        <p class="px-3 py-4 text-xs text-zinc-600 text-center">No terminals</p>
      {/if}
    </div>

    <div class="p-2 border-t border-zinc-800">
      <button
        onclick={onNewTerminal}
        class="w-full flex items-center gap-1.5 px-2 py-1.5 rounded text-xs text-zinc-500 hover:text-zinc-200 hover:bg-zinc-800 transition"
      >
        <svg class="w-3.5 h-3.5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        New Terminal
      </button>
    </div>
  </aside>

  <!-- ── Terminal instances ── -->
  <div class="flex-1 overflow-hidden relative bg-black">
    {#each channels as ch (ch.chanID)}
      <div
        class="absolute inset-0"
        style="visibility: {activeChannel === ch.chanID ? 'visible' : 'hidden'}"
      >
        <Terminal chanID={ch.chanID} client={client} connectCount={connectCount} />
      </div>
    {/each}

    {#if channels.length === 0}
      <div class="flex h-full items-center justify-center flex-col gap-3 text-zinc-700">
        <svg class="w-12 h-12 opacity-20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="2" y="3" width="20" height="15" rx="2"/>
          <polyline points="8 10 12 14 8 18"/>
          <line x1="16" y1="18" x2="12" y2="18"/>
        </svg>
        <p class="text-sm">No terminals open</p>
        <button
          onclick={onNewTerminal}
          class="text-xs px-3 py-1.5 rounded bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 text-zinc-400 hover:text-zinc-100 transition"
        >Open Terminal</button>
      </div>
    {/if}
  </div>
</div>

{#snippet tabItem(ch: PTYChannel, isPinned: boolean)}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="group relative mx-1.5 mb-0.5">
    <div class="flex items-center rounded transition
      {activeChannel === ch.chanID
        ? 'bg-blue-600/25 text-zinc-100 ring-1 ring-inset ring-blue-600/40'
        : 'text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200'}">

      <div class="pl-2 py-1.5 shrink-0 opacity-60">
        <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <polyline points="4 17 10 11 4 5"/>
          <line x1="12" y1="19" x2="20" y2="19"/>
        </svg>
      </div>

      {#if renamingChanID === ch.chanID}
        <!-- svelte-ignore a11y_autofocus -->
        <input
          type="text"
          autofocus
          bind:value={renameValue}
          class="flex-1 min-w-0 mx-1.5 my-1 text-xs bg-zinc-800 border border-blue-500 rounded px-1.5 py-0.5 outline-none text-zinc-100"
          onclick={(e) => e.stopPropagation()}
          onblur={commitRename}
          onkeydown={(e) => {
            e.stopPropagation();
            if (e.key === 'Enter') commitRename();
            if (e.key === 'Escape') { renamingChanID = null; }
          }}
        />
      {:else}
        <button
          class="flex-1 min-w-0 text-left text-xs py-1.5 pl-1.5 pr-1 truncate"
          onclick={() => onSelectChannel(ch.chanID)}
          ondblclick={() => { if (activeChannel === ch.chanID) startRename(ch.chanID, ch.label); }}
        >{ch.label}</button>
      {/if}

      {#if renamingChanID !== ch.chanID}
        <div class="flex items-center opacity-0 group-hover:opacity-100 pr-1 gap-0.5 shrink-0">
          <button
            onclick={(e) => { e.stopPropagation(); isPinned ? onUnpinChannel(ch.chanID) : onPinChannel(ch.chanID); }}
            title={isPinned ? 'Unpin' : 'Pin'}
            class="p-0.5 rounded hover:bg-zinc-700 transition {isPinned ? 'text-yellow-400' : 'text-zinc-500 hover:text-zinc-300'}"
          >
            <svg class="w-3 h-3" viewBox="0 0 24 24" fill={isPinned ? 'currentColor' : 'none'} stroke="currentColor" stroke-width="2">
              <line x1="12" y1="17" x2="12" y2="22"/>
              <path d="M5 17h14v-1.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V6h1a2 2 0 0 0 0-4H8a2 2 0 0 0 0 4h1v4.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24Z"/>
            </svg>
          </button>
          <button
            onclick={(e) => { e.stopPropagation(); onCloseChannel(ch.chanID); }}
            title="Close"
            class="p-0.5 rounded hover:bg-red-500/20 hover:text-red-400 text-zinc-500 transition"
          >
            <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>
      {/if}
    </div>
  </div>
{/snippet}
