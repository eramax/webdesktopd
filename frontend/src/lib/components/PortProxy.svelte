<script lang="ts">
  import type { WSClient } from '$lib/client';
  import { FrameType, decodeJSON } from '$lib/protocol';
  import { onMount, onDestroy } from 'svelte';

  interface Props {
    client: WSClient;
  }

  let { client }: Props = $props();

  interface PortInfo {
    port: number;
    pid: number;
    process: string;
    cmdline: string;
  }

  let ports = $state<PortInfo[]>([]);
  let activePort = $state<number | null>(null);
  let scanning = $state(false);
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  function scan() {
    scanning = true;
    client.sendJSON(FrameType.PortScan, 0, {});
  }

  function toggle(port: number) {
    activePort = activePort === port ? null : port;
  }

  function proxyURL(port: number): string {
    return `/_proxy/${port}/`;
  }

  onMount(() => {
    client.registerBroadcast('port-proxy-scan', (frame) => {
      if (frame.type !== FrameType.PortScanResp) return;
      scanning = false;
      try {
        const resp = decodeJSON<{ ports: PortInfo[] | null }>(frame.payload);
        ports = resp.ports ?? [];
      } catch {
        ports = [];
      }
    });
    scan();
    pollTimer = setInterval(scan, 4000);
  });

  onDestroy(() => {
    client.unregisterBroadcast('port-proxy-scan');
    if (pollTimer) clearInterval(pollTimer);
  });
</script>

<div class="flex h-full bg-zinc-950">
  <!-- Sidebar: compact port list -->
  <aside class="w-64 border-r border-zinc-800 flex flex-col bg-zinc-900 shrink-0">
    <div class="px-3 py-2 border-b border-zinc-800 flex items-center justify-between">
      <p class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">Listening Ports</p>
      <button onclick={scan} class="text-zinc-500 hover:text-zinc-300 transition" title="Refresh">
        <svg class="w-3.5 h-3.5 {scanning ? 'animate-spin' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M23 4v6h-6"/><path d="M1 20v-6h6"/>
          <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>
        </svg>
      </button>
    </div>

    <div class="flex-1 overflow-y-auto">
      {#if ports.length === 0}
        <p class="px-3 py-4 text-xs text-zinc-600 text-center">
          {scanning ? 'Scanning…' : 'No listening ports found.'}
        </p>
      {:else}
        {#each ports as info (info.port)}
          <div class="flex items-center gap-2 px-3 py-2 border-b border-zinc-800/50 hover:bg-zinc-800/40 transition
            {activePort === info.port ? 'bg-zinc-800/60' : ''}">
            <!-- Port + process info (clickable) -->
            <button
              class="flex-1 min-w-0 text-left"
              onclick={() => toggle(info.port)}
              title={info.cmdline || `port ${info.port}`}
            >
              <div class="flex items-center gap-2 min-w-0">
                <span class="shrink-0 font-mono text-[11px] text-blue-400 bg-blue-950/60 px-1.5 py-0.5 rounded">
                  :{info.port}
                </span>
                <div class="min-w-0">
                  <p class="text-xs text-zinc-300 truncate leading-tight">
                    {info.process || 'unknown'}
                  </p>
                  {#if info.cmdline && info.cmdline !== info.process}
                    <p class="text-[10px] text-zinc-600 font-mono truncate leading-tight">
                      {info.cmdline}
                    </p>
                  {/if}
                </div>
              </div>
            </button>
            <!-- Globe toggle: open/close iframe -->
            <button
              onclick={() => toggle(info.port)}
              class="shrink-0 p-1 rounded transition
                {activePort === info.port
                  ? 'text-blue-400 bg-blue-950/60 hover:bg-blue-900/60'
                  : 'text-zinc-600 hover:text-zinc-300'}"
              title={activePort === info.port ? 'Close' : 'Open in frame'}
            >
              <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="12" cy="12" r="10"/>
                <line x1="2" y1="12" x2="22" y2="12"/>
                <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
              </svg>
            </button>
          </div>
        {/each}
      {/if}
    </div>
  </aside>

  <!-- Main: iframe or empty state -->
  <div class="flex-1 flex flex-col overflow-hidden">
    {#if activePort !== null}
      <div class="flex items-center gap-2 px-3 py-1.5 bg-zinc-900 border-b border-zinc-800 shrink-0 text-[10px] font-mono text-zinc-500">
        <span class="flex-1">127.0.0.1:{activePort} → /_proxy/{activePort}/</span>
        <a
          href={proxyURL(activePort)}
          target="_blank"
          rel="noopener noreferrer"
          class="text-blue-400 hover:text-blue-300 not-italic text-xs"
        >Open ↗</a>
      </div>
      <iframe
        src={proxyURL(activePort)}
        title="Port {activePort}"
        class="flex-1 w-full border-none bg-white"
        sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals"
      ></iframe>
    {:else}
      <div class="flex-1 flex items-center justify-center text-zinc-600 text-sm flex-col gap-2">
        <svg class="w-12 h-12 opacity-20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="12" cy="12" r="10"/>
          <line x1="2" y1="12" x2="22" y2="12"/>
          <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
        </svg>
        <p>Click the globe icon next to any port</p>
        <p class="text-xs text-zinc-700">List refreshes every 4 seconds</p>
      </div>
    {/if}
  </div>
</div>
