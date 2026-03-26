<script lang="ts">
  import type { WSClient } from '$lib/client';
  import { FrameType } from '$lib/protocol';

  interface Props {
    chanID: number;
    client: WSClient;
  }

  let { chanID, client }: Props = $props();

  let containerEl = $state<HTMLDivElement | undefined>(undefined);

  $effect(() => {
    // Re-run when chanID or client changes.
    const _chanID = chanID;
    const _client = client;
    const _container = containerEl;

    if (!_container) return;

    let term: import('@xterm/xterm').Terminal | undefined;
    let fitAddon: import('@xterm/addon-fit').FitAddon | undefined;
    let resizeObserver: ResizeObserver | undefined;

    // Dynamic import so xterm.js is only loaded client-side.
    async function init() {
      const { Terminal } = await import('@xterm/xterm');
      const { FitAddon } = await import('@xterm/addon-fit');
      await import('@xterm/xterm/css/xterm.css');

      term = new Terminal({
        theme: {
          background: '#000000',
          foreground: '#ffffff',
          cursor: '#ffffff',
          black: '#000000',
          brightBlack: '#555555',
          red: '#cd3131',
          brightRed: '#f14c4c',
          green: '#0dbc79',
          brightGreen: '#23d18b',
          yellow: '#e5e510',
          brightYellow: '#f5f543',
          blue: '#2472c8',
          brightBlue: '#3b8eea',
          magenta: '#bc3fbc',
          brightMagenta: '#d670d6',
          cyan: '#11a8cd',
          brightCyan: '#29b8db',
          white: '#e5e5e5',
          brightWhite: '#e5e5e5',
        },
        fontFamily: 'monospace',
        fontSize: 14,
        cursorBlink: true,
        allowTransparency: false,
        scrollback: 5000,
      });

      fitAddon = new FitAddon();
      term.loadAddon(fitAddon);
      term.open(_container);
      fitAddon.fit();
      term.focus();

      // Send terminal output to the server as Data frames
      term.onData((data: string) => {
        const encoded = new TextEncoder().encode(data);
        _client.send({
          type: FrameType.Data,
          chanID: _chanID,
          payload: encoded,
        });
      });

      // Notify server of terminal size changes
      term.onResize(({ cols, rows }: { cols: number; rows: number }) => {
        _client.resizePTY(_chanID, cols, rows);
      });

      // Capture current dimensions. Resize frames sent before openPTY are
      // dropped (no server handler yet), so we pass dims inside the openPTY
      // payload so the server can set the initial PTY size immediately.
      const dims = fitAddon.proposeDimensions();

      // Receive data from server — register BEFORE sending openPTY so no
      // output is missed (server sends ring buffer replay immediately on open).
      _client.register(_chanID, (frame) => {
        if (frame.type === FrameType.Data) {
          term?.write(frame.payload);
        }
      });

      // Request the PTY from the server (or re-attach if it already exists),
      // including the initial terminal dimensions.
      _client.openPTY(_chanID, undefined, undefined, dims?.cols, dims?.rows);

      // Observe container size changes for auto-fit
      resizeObserver = new ResizeObserver(() => {
        if (fitAddon && term) {
          fitAddon.fit();
        }
      });
      resizeObserver.observe(_container);
    }

    init().catch((err) => console.error('[Terminal] init error:', err));

    // Cleanup
    return () => {
      resizeObserver?.disconnect();
      _client.unregister(_chanID);
      term?.dispose();
    };
  });
</script>

<div bind:this={containerEl} class="w-full h-full overflow-hidden"></div>
