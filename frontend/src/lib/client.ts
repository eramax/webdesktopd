import { encodeFrame, decodeFrame, FrameType, encodeJSON, type Frame } from './protocol';

export type ChannelHandler = (frame: Frame) => void;

const RECONNECT_DELAY_MS = 2000;
const HEARTBEAT_INTERVAL_MS = 3000;
const HEARTBEAT_TIMEOUT_MS = 5000;

export class WSClient {
  private ws: WebSocket | null = null;
  private heartbeatTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatTimeout: ReturnType<typeof setTimeout> | null = null;
  private awaitingPong = false;
  /**
   * Primary per-channel handlers (one per chanID).
   * chanID 0 is special: it also delivers to all broadcast listeners.
   */
  private handlers = new Map<number, ChannelHandler>();
  /**
   * Broadcast listeners: receive every frame on chanID 0, keyed by a
   * caller-supplied tag so multiple components can subscribe without
   * overwriting each other.
   */
  private broadcastListeners = new Map<string, ChannelHandler>();
  private token: string;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private shouldReconnect = true;

  // Callbacks
  onOpen?: () => void;
  onClose?: () => void;
  onError?: (err: Event) => void;
  onBackendActivity?: () => void;
  /** Called when the server is reachable but rejects our token (e.g. after restart with new JWT secret). */
  onAuthError?: () => void;

  constructor(token: string) {
    this.token = token;
  }

  /**
   * Open the WebSocket connection to /ws?token=JWT.
   * Automatically reconnects after 2s on unexpected close.
   */
  connect(): void {
    this.shouldReconnect = true;
    this._openSocket();
  }

  private _startHeartbeat(): void {
    this._stopHeartbeat();
    this.awaitingPong = false;
    this.heartbeatTimer = setTimeout(() => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        return;
      }
      this.awaitingPong = true;
      this.sendJSON(FrameType.Ping, 0, {});

      this.heartbeatTimeout = setTimeout(() => {
        if (!this.awaitingPong) return;
        this.awaitingPong = false;
        this._markDisconnected();
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          this.ws.close();
        }
      }, HEARTBEAT_TIMEOUT_MS);
    }, HEARTBEAT_INTERVAL_MS);
  }

  private _stopHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      clearTimeout(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
    if (this.heartbeatTimeout !== null) {
      clearTimeout(this.heartbeatTimeout);
      this.heartbeatTimeout = null;
    }
    this.awaitingPong = false;
  }

  private _handlePong(): void {
    this.awaitingPong = false;
    if (this.heartbeatTimeout !== null) {
      clearTimeout(this.heartbeatTimeout);
      this.heartbeatTimeout = null;
    }
    this.onBackendActivity?.();
    this._startHeartbeat();
  }

  private _markDisconnected(): void {
    this.onClose?.();
  }

  private _openSocket(): void {
    if (this.ws) {
      // Already connected or connecting – do nothing.
      return;
    }

    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${protocol}://${window.location.host}/ws?token=${encodeURIComponent(this.token)}`;

    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    this.ws = ws;
    let opened = false;

    ws.addEventListener('open', () => {
      opened = true;
      this.onOpen?.();
      this._startHeartbeat();
    });

    ws.addEventListener('message', (event: MessageEvent<ArrayBuffer>) => {
      try {
        const frame = decodeFrame(event.data);
        if (frame.type === FrameType.Pong) {
          this._handlePong();
          return;
        }
        this.onBackendActivity?.();
        this._startHeartbeat();
        if (frame.chanID === 0) {
          // Deliver to the primary handler (if any) then all broadcast listeners
          this.handlers.get(0)?.(frame);
          for (const listener of this.broadcastListeners.values()) {
            listener(frame);
          }
        } else {
          const handler = this.handlers.get(frame.chanID);
          handler?.(frame);
        }
      } catch (err) {
        console.error('[WSClient] Failed to decode frame:', err);
      }
    });

    ws.addEventListener('close', async () => {
      this.ws = null;
      this._stopHeartbeat();
      this._markDisconnected();
      this.onClose?.();

      if (!this.shouldReconnect) return;

      if (!opened) {
        try {
          const controller = new AbortController();
          const timeoutId = setTimeout(() => controller.abort(), 3000);
          const resp = await fetch(`/validate?token=${encodeURIComponent(this.token)}`, {
            signal: controller.signal,
          });
          clearTimeout(timeoutId);
          if (resp.status === 401) {
            this.shouldReconnect = false;
            this.onAuthError?.();
            return;
          }
        } catch {
          // Server not reachable yet or validation timed out — retry normally.
        }
      }

      this.reconnectTimer = setTimeout(() => {
        this.reconnectTimer = null;
        this._openSocket();
      }, RECONNECT_DELAY_MS);
    });

    ws.addEventListener('error', (err: Event) => {
      this.onError?.(err);
      // The 'close' event will fire after an error, handling reconnect there.
    });
  }

  /**
   * Close the WebSocket without triggering reconnect.
   */
  disconnect(): void {
    this.shouldReconnect = false;

    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }

    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }

    this._stopHeartbeat();
  }

  /**
   * Encode and send a frame over the WebSocket.
   * Silently drops the frame if the socket is not open.
   */
  send(frame: Frame): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return;
    }
    this.ws.send(encodeFrame(frame));
  }

  /** Convenience: build and send a JSON-payload frame. */
  sendJSON(type: number, chanID: number, obj: unknown): void {
    this.send({
      type: type as Frame['type'],
      chanID,
      payload: encodeJSON(obj),
    });
  }

  /**
   * Register a handler for a specific chanID.
   * For chanID > 0, only one handler per channel is supported.
   * For chanID = 0 use registerBroadcast() to avoid overwriting other subscribers.
   */
  register(chanID: number, handler: ChannelHandler): void {
    this.handlers.set(chanID, handler);
  }

  unregister(chanID: number): void {
    this.handlers.delete(chanID);
  }

  /**
   * Subscribe to all chanID=0 frames without displacing other subscribers.
   * @param tag  Unique string identifying the caller (used to unregister).
   */
  registerBroadcast(tag: string, handler: ChannelHandler): void {
    this.broadcastListeners.set(tag, handler);
  }

  unregisterBroadcast(tag: string): void {
    this.broadcastListeners.delete(tag);
  }

  /**
   * Send an OpenPTY (0x0A) frame to request a new PTY from the server.
   */
  openPTY(chanID: number, shell?: string, cwd?: string, cols?: number, rows?: number): void {
    // OpenPTY is a control-plane message: always sent on chanID 0.
    // The payload carries the target chanID for the server to route correctly.
    // cols/rows set the initial PTY size immediately — send them here because
    // resize frames sent before openPTY are dropped (no handler yet).
    this.sendJSON(FrameType.OpenPTY, 0, {
      channel: chanID,
      shell: shell ?? '/bin/bash',
      cwd: cwd ?? '',
      cols: cols ?? 0,
      rows: rows ?? 0,
    });
  }

  /**
   * Send a PTYResize (0x02) frame to notify the server of terminal dimensions.
   */
  resizePTY(chanID: number, cols: number, rows: number): void {
    this.sendJSON(FrameType.PTYResize, chanID, {
      cols,
      rows,
      channel: chanID,
    });
  }
}
