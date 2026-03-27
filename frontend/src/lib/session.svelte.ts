import { WSClient } from './client';

export interface PTYChannel {
  chanID: number;
  label: string;
}

export interface ProxyChannel {
  chanID: number;
  target: string;
  label: string;
}

class SessionStore {
  token = $state<string | null>(null);
  username = $state<string | null>(null);
  client = $state<WSClient | null>(null);
  connected = $state(false);
  /** Increments on every successful WebSocket open (first connect + every reconnect). */
  connectCount = $state(0);
  ptyChannels = $state<PTYChannel[]>([]);
  activeChannel = $state<number | null>(null);

  /** User's home directory, set from session sync frame. */
  homeDir = $state<string | null>(null);
  /** Which app occupies the main area: 'terminal', 'files', or 'proxy'. */
  activeApp = $state<'terminal' | 'files' | 'proxy'>('terminal');
  /** Whether the file manager is open. */
  fileManagerOpen = $state(false);

  /** Active port proxy channels. */
  proxyChannels = $state<ProxyChannel[]>([]);
  /** Whether the port proxy panel is open. */
  proxyManagerOpen = $state(false);
  /** Active proxy chanID shown in proxy panel. */
  activeProxyChanID = $state<number | null>(null);

  /** Current wallpaper CSS value (background shorthand). */
  wallpaper = $state<string>('');

  private _nextID = 1;

  login(username: string, token: string): void {
    this.token = token;
    this.username = username;

    const ws = new WSClient(token);
    ws.onOpen = () => {
      this.connected = true;
      this.connectCount++;
    };
    ws.onClose = () => {
      this.connected = false;
    };
    this.client = ws;
  }

  logout(): void {
    this.client?.disconnect();
    this.client = null;
    this.token = null;
    this.username = null;
    this.connected = false;
    this.connectCount = 0;
    this.ptyChannels = [];
    this.activeChannel = null;
    this.homeDir = null;
    this.activeApp = 'terminal';
    this.fileManagerOpen = false;
    this.proxyChannels = [];
    this.proxyManagerOpen = false;
    this.activeProxyChanID = null;
    this.wallpaper = '';
    this._nextID = 1;
    // Clear auth cookie
    document.cookie = 'wdd_token=; path=/; max-age=0';
  }

  openFileManager(): void {
    this.fileManagerOpen = true;
    this.activeApp = 'files';
  }

  closeFileManager(): void {
    this.fileManagerOpen = false;
    if (this.activeApp === 'files') {
      this.activeApp = 'terminal';
    }
  }

  openProxyManager(): void {
    this.proxyManagerOpen = true;
    this.activeApp = 'proxy';
  }

  closeProxyManager(): void {
    this.proxyManagerOpen = false;
    if (this.activeApp === 'proxy') {
      this.activeApp = this.ptyChannels.length > 0 ? 'terminal' : 'files';
    }
  }

  addPTYChannel(chanID: number, label?: string): void {
    const resolvedLabel = label ?? `Terminal ${chanID}`;
    if (!this.ptyChannels.find((c) => c.chanID === chanID)) {
      this.ptyChannels = [...this.ptyChannels, { chanID, label: resolvedLabel }];
    }
  }

  removePTYChannel(chanID: number): void {
    this.ptyChannels = this.ptyChannels.filter((c) => c.chanID !== chanID);
    if (this.activeChannel === chanID) {
      this.activeChannel = this.ptyChannels[0]?.chanID ?? null;
    }
  }

  setActiveChannel(chanID: number): void {
    this.activeChannel = chanID;
  }

  addProxyChannel(chanID: number, target: string, label?: string): void {
    const resolvedLabel = label ?? `Proxy :${target.split(':').pop()}`;
    if (!this.proxyChannels.find((c) => c.chanID === chanID)) {
      this.proxyChannels = [...this.proxyChannels, { chanID, target, label: resolvedLabel }];
    }
  }

  removeProxyChannel(chanID: number): void {
    this.proxyChannels = this.proxyChannels.filter((c) => c.chanID !== chanID);
    if (this.activeProxyChanID === chanID) {
      this.activeProxyChanID = this.proxyChannels[0]?.chanID ?? null;
    }
  }

  /** Returns a new unique channel ID (increments monotonically). */
  nextChannelID(): number {
    const id = this._nextID;
    this._nextID++;
    return id;
  }
}

export const session = new SessionStore();
