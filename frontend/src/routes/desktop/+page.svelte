<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { session } from '$lib/session.svelte';
  import { FrameType } from '$lib/protocol';
  import type { Frame } from '$lib/protocol';
  import Terminal from '$lib/components/Terminal.svelte';
  import FileManager from '$lib/components/FileManager.svelte';
  import PortProxy from '$lib/components/PortProxy.svelte';
  import Dock from '$lib/components/Dock.svelte';

  // Debounce helper
  function debounce<T extends (...args: unknown[]) => void>(fn: T, ms: number): T {
    let timer: ReturnType<typeof setTimeout> | null = null;
    return ((...args: Parameters<T>) => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => { timer = null; fn(...args); }, ms);
    }) as T;
  }

  const saveState = debounce(() => {
    if (!session.client || !session.connected) return;
    const state = {
      wallpaper: session.wallpaper,
      tabs: session.ptyChannels.map((c) => ({ chanID: c.chanID, label: c.label })),
      windows: [],
    };
    const enc = new TextEncoder();
    session.client.send({
      type: FrameType.DesktopSave,
      chanID: 0,
      payload: enc.encode(JSON.stringify(state)),
    });
  }, 500);

  function concatBytes(arrays: Uint8Array[]): Uint8Array {
    const total = arrays.reduce((s, a) => s + a.length, 0);
    const out = new Uint8Array(total);
    let offset = 0;
    for (const a of arrays) { out.set(a, offset); offset += a.length; }
    return out;
  }

  function parseHTTPResponse(bytes: Uint8Array): {
    status: number;
    headers: Record<string, string>;
    body: Uint8Array;
  } {
    // Find header/body separator.
    let sepIdx = -1;
    for (let i = 0; i < bytes.length - 3; i++) {
      if (bytes[i] === 13 && bytes[i+1] === 10 && bytes[i+2] === 13 && bytes[i+3] === 10) {
        sepIdx = i;
        break;
      }
    }
    if (sepIdx === -1) {
      return { status: 502, headers: {}, body: bytes };
    }

    const headerBytes = bytes.slice(0, sepIdx);
    const body = bytes.slice(sepIdx + 4);
    const headerText = new TextDecoder('iso-8859-1').decode(headerBytes);
    const lines = headerText.split('\r\n');

    const statusParts = lines[0].split(' ');
    const status = parseInt(statusParts[1] ?? '200', 10) || 200;

    const headers: Record<string, string> = {};
    for (const line of lines.slice(1)) {
      const idx = line.indexOf(':');
      if (idx > 0) {
        const key = line.slice(0, idx).trim().toLowerCase();
        const val = line.slice(idx + 1).trim();
        // Skip transfer-encoding (we send raw body, not chunked)
        if (key !== 'transfer-encoding') {
          headers[key] = val;
        }
      }
    }

    return { status, headers, body };
  }

  onMount(() => {
    if (!session.token || !session.client) {
      goto('/');
      return;
    }

    // Set auth cookie so /_proxy/ iframe requests are authenticated.
    document.cookie = `wdd_token=${session.token}; path=/; SameSite=Lax`;

    // Connect WebSocket
    session.client.connect();

    // Register service worker for port proxy iframe support.
    if ('serviceWorker' in navigator) {
      navigator.serviceWorker.register('/sw.js').catch((err) => {
        console.warn('[desktop] SW registration failed:', err);
      });
    }

    // Handle proxy HTTP requests from the Service Worker.
    if ('serviceWorker' in navigator) {
      navigator.serviceWorker.addEventListener('message', (event) => {
        if (event.data?.type !== 'proxy-http-request') return;
        const { port, requestBytes } = event.data as { port: string; requestBytes: Uint8Array };
        const replyPort = event.ports[0];
        if (!session.client) {
          replyPort.postMessage({ error: 'No WS client' });
          return;
        }

        const chanID = session.nextChannelID();
        const target = `127.0.0.1:${port}`;

        // Open proxy WS channel.
        session.client.sendJSON(FrameType.OpenProxy, 0, { channel: chanID, target });

        // Collect response chunks.
        const chunks: Uint8Array[] = [];

        session.client.register(chanID, (frame) => {
          if (frame.type === FrameType.Data) {
            chunks.push(frame.payload);
          } else if (frame.type === FrameType.CloseProxy) {
            // TCP connection closed — parse and return HTTP response.
            session.client?.unregister(chanID);
            const response = parseHTTPResponse(concatBytes(chunks));
            replyPort.postMessage(response);
          }
        });

        // Send the HTTP request bytes.
        session.client.send({ type: FrameType.Data, chanID, payload: new Uint8Array(requestBytes) });
      });
    }

    // Handle session sync from server: restore PTY channels, proxy channels, homeDir, desktop state.
    session.client.registerBroadcast('session-sync', (frame: Frame) => {
      if (frame.type === FrameType.SessionSync) {
        try {
          const state = JSON.parse(new TextDecoder().decode(frame.payload)) as {
            ptyChannels?: Array<{ chanID: number; username?: string }>;
            proxyChannels?: Array<{ chanID: number; target: string }>;
            homeDir?: string;
            desktopState?: { wallpaper?: string; tabs?: Array<{ chanID: number; label: string }> };
          };
          if (state.homeDir) session.homeDir = state.homeDir;
          if (state.ptyChannels) {
            for (const ch of state.ptyChannels) {
              session.addPTYChannel(ch.chanID, `Terminal ${ch.chanID}`);
            }
            if (session.ptyChannels.length > 0 && session.activeChannel === null) {
              session.setActiveChannel(session.ptyChannels[0].chanID);
            }
          }
          if (state.proxyChannels) {
            for (const ch of state.proxyChannels) {
              session.addProxyChannel(ch.chanID, ch.target);
            }
          }
          if (state.desktopState) {
            if (state.desktopState.wallpaper != null) {
              session.wallpaper = state.desktopState.wallpaper;
            }
            // Restore tab labels from state.
            if (state.desktopState.tabs) {
              for (const tab of state.desktopState.tabs) {
                const ch = session.ptyChannels.find((c) => c.chanID === tab.chanID);
                if (ch) ch.label = tab.label;
              }
            }
          }
        } catch {
          // ignore malformed sync frames
        }
      }
    });

    // Handle server-pushed desktop state (0x12).
    session.client.registerBroadcast('desktop-push', (frame: Frame) => {
      if (frame.type === FrameType.DesktopPush) {
        try {
          const state = JSON.parse(new TextDecoder().decode(frame.payload)) as {
            wallpaper?: string;
          };
          if (state.wallpaper != null) session.wallpaper = state.wallpaper;
        } catch { /* ignore */ }
      }
    });

    // Handle server-initiated proxy close (TCP connection closed).
    session.client.registerBroadcast('proxy-close', (frame: Frame) => {
      if (frame.type === FrameType.CloseProxy) {
        try {
          const msg = JSON.parse(new TextDecoder().decode(frame.payload)) as { channel: number };
          if (msg.channel) {
            session.removeProxyChannel(msg.channel);
            session.client?.unregister(msg.channel);
          }
        } catch { /* ignore */ }
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
      session.client?.unregisterBroadcast('desktop-push');
      session.client?.unregisterBroadcast('proxy-close');
    };
  });

  function openNewTerminal() {
    if (!session.client) return;
    const chanID = session.nextChannelID();
    session.addPTYChannel(chanID, `Terminal ${chanID}`);
    session.setActiveChannel(chanID);
    session.activeApp = 'terminal';
  }

  function closeTerminal(chanID: number) {
    if (!session.client) return;
    session.client.sendJSON(FrameType.ClosePTY, 0, { channel: chanID });
    session.client.unregister(chanID);
    session.removePTYChannel(chanID);
  }

  function openFiles() {
    session.openFileManager();
  }

  function closeFiles() {
    session.closeFileManager();
  }

  function openProxyManager() {
    session.openProxyManager();
  }

  function closeProxyManager() {
    session.closeProxyManager();
  }

  function selectChannel(chanID: number) {
    session.setActiveChannel(chanID);
    session.activeApp = 'terminal';
  }

  function onWallpaperChange(wallpaper: string) {
    session.wallpaper = wallpaper;
    saveState();
  }

  let connected = $derived(session.connected);
  let effectiveHomeDir = $derived(session.homeDir ?? '/');

  // Wallpaper presets
  const wallpapers = [
    { label: 'Dark', value: '' },
    { label: 'Navy', value: 'linear-gradient(135deg,#0f172a 0%,#1e3a5f 100%)' },
    { label: 'Forest', value: 'linear-gradient(135deg,#064e3b 0%,#065f46 100%)' },
    { label: 'Dusk', value: 'linear-gradient(135deg,#312e81 0%,#4c1d95 100%)' },
    { label: 'Slate', value: 'linear-gradient(135deg,#1e293b 0%,#334155 100%)' },
    { label: 'Crimson', value: 'linear-gradient(135deg,#450a0a 0%,#7f1d1d 100%)' },
  ];
</script>

<svelte:head>
  <title>webdesktopd</title>
</svelte:head>

<!-- Full-screen desktop -->
<div
  class="flex flex-col h-screen text-zinc-100 overflow-hidden"
  style={session.wallpaper ? `background: ${session.wallpaper}` : undefined}
  class:bg-zinc-950={!session.wallpaper}
>

  <!-- Thin top bar -->
  <header class="flex items-center justify-between px-4 py-1.5 bg-zinc-900/80 border-b border-zinc-800 shrink-0 backdrop-blur">
    <span class="font-semibold text-zinc-200 text-sm tracking-tight">webdesktopd</span>

    <div class="flex items-center gap-3">
      <!-- Wallpaper picker -->
      <div class="flex items-center gap-0.5">
        {#each wallpapers as wp}
          <button
            title={wp.label}
            onclick={() => onWallpaperChange(wp.value)}
            class="w-4 h-4 rounded-sm border transition
              {session.wallpaper === wp.value ? 'border-blue-400 ring-1 ring-blue-400' : 'border-zinc-600 hover:border-zinc-400'}"
            style={wp.value ? `background: ${wp.value}` : 'background: #09090b'}
          ></button>
        {/each}
      </div>

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

  <!-- Main app area -->
  <main class="flex-1 overflow-hidden relative">
    {#if session.client}
      <!-- Terminals: always mounted, visibility toggles -->
      {#each session.ptyChannels as ch (ch.chanID)}
        <div
          class="absolute inset-0"
          style="visibility: {session.activeApp === 'terminal' && session.activeChannel === ch.chanID ? 'visible' : 'hidden'}"
        >
          <Terminal chanID={ch.chanID} client={session.client} connectCount={session.connectCount} />
        </div>
      {/each}

      <!-- File manager -->
      {#if session.fileManagerOpen}
        <div
          class="absolute inset-0"
          style="visibility: {session.activeApp === 'files' ? 'visible' : 'hidden'}"
        >
          <FileManager client={session.client} homeDir={effectiveHomeDir} />
        </div>
      {/if}

      <!-- Port proxy panel -->
      {#if session.proxyManagerOpen}
        <div
          class="absolute inset-0"
          style="visibility: {session.activeApp === 'proxy' ? 'visible' : 'hidden'}"
        >
          <PortProxy client={session.client} />
        </div>
      {/if}

      <!-- Empty state -->
      {#if session.ptyChannels.length === 0 && !session.fileManagerOpen && !session.proxyManagerOpen}
        <div class="flex h-full items-center justify-center text-zinc-700 text-sm">
          No app open — use the dock to open a terminal or file manager
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
      activeApp={session.activeApp}
      fileManagerOpen={session.fileManagerOpen}
      proxyManagerOpen={session.proxyManagerOpen}
      onNewTerminal={openNewTerminal}
      onSelectChannel={selectChannel}
      onCloseChannel={closeTerminal}
      onOpenFiles={openFiles}
      onCloseFiles={closeFiles}
      onOpenProxy={openProxyManager}
      onCloseProxy={closeProxyManager}
    />
  {/if}
</div>
