<script lang="ts">
  import type { WSClient } from '$lib/client';
  import { FrameType } from '$lib/protocol';

  interface Props {
    chanID: number;
    client: WSClient;
    /** Increments on every WS open (first connect + every reconnect). */
    connectCount: number;
  }

  let { chanID, client, connectCount }: Props = $props();

  let containerEl = $state<HTMLDivElement | undefined>(undefined);

  /**
   * Set by the init effect once xterm is ready; cleared on cleanup.
   * The reconnect effect reads this to know when it can send openPTY.
   */
  let termRef = $state<{
    term: import('@xterm/xterm').Terminal;
    fitAddon: import('@xterm/addon-fit').FitAddon;
  } | null>(null);

  /**
   * Init effect: creates the xterm.js terminal, registers the data handler,
   * and observes container size. Runs when chanID, client, or container changes.
   * Does NOT send openPTY — that is handled by the reconnect effect below.
   */
  $effect(() => {
    const _chanID = chanID;
    const _client = client;
    const _container = containerEl;

    if (!_container) return;

    let resizeObserver: ResizeObserver | undefined;

    async function init() {
      const { Terminal } = await import('@xterm/xterm');
      const { FitAddon } = await import('@xterm/addon-fit');
      await import('@xterm/xterm/css/xterm.css');

      const term = new Terminal({
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

      const fitAddon = new FitAddon();
      term.loadAddon(fitAddon);
      term.open(_container!);
      fitAddon.fit();
      term.focus();

      // Send keyboard input to server as Data frames.
      term.onData((data: string) => {
        const encoded = new TextEncoder().encode(data);
        _client.send({
          type: FrameType.Data,
          chanID: _chanID,
          payload: encoded,
        });
      });

      // Notify server of terminal size changes.
      term.onResize(({ cols, rows }: { cols: number; rows: number }) => {
        _client.resizePTY(_chanID, cols, rows);
      });

      // Receive data from server — register BEFORE setting termRef so the
      // reconnect effect cannot send openPTY before the handler is ready.
      _client.register(_chanID, (frame) => {
        if (frame.type === FrameType.Data) {
          term.write(frame.payload);
        }
      });

      // Observe container size changes for auto-fit.
      resizeObserver = new ResizeObserver(() => fitAddon.fit());
      resizeObserver.observe(_container!);

      // Signal readiness. The reconnect effect fires once this is set.
      termRef = { term, fitAddon };
    }

    init().catch((err) => console.error('[Terminal] init error:', err));

    return () => {
      resizeObserver?.disconnect();
      _client.unregister(_chanID);
      termRef?.term.dispose();
      termRef = null;
    };
  });

  /**
   * Reconnect effect: sends openPTY whenever the WebSocket (re)connects AND
   * the terminal is ready. Fires on:
   *   • first connect after init completes (termRef becomes non-null),
   *   • every subsequent reconnect (connectCount increments while termRef is set).
   *
   * The server handles openPTY idempotently: creates a new PTY on first call,
   * re-attaches and replays the ring buffer on subsequent calls.
   */
  $effect(() => {
    const _count = connectCount;
    const _ref = termRef;
    const _client = client;
    const _chanID = chanID;

    if (_count === 0 || !_ref) return;

    const dims = _ref.fitAddon.proposeDimensions();
    _client.openPTY(_chanID, undefined, undefined, dims?.cols, dims?.rows);
  });
</script>

<div class="w-full h-full overflow-hidden p-2 bg-black">
  <div bind:this={containerEl} class="w-full h-full overflow-hidden"></div>
</div>
