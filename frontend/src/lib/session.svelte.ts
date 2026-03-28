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
  /** chanIDs of pinned terminal tabs. */
  pinnedTerminals = $state<number[]>([]);

  /** User's home directory, set from session sync frame. */
  homeDir = $state<string | null>(null);
  /** Which app occupies the main area. */
  activeApp = $state<'terminal' | 'files' | 'proxy'>('terminal');

  /** Active port proxy channels. */
  proxyChannels = $state<ProxyChannel[]>([]);
  /** Active proxy chanID shown in proxy panel. */
  activeProxyChanID = $state<number | null>(null);

  /** Current wallpaper CSS value. */
  wallpaper = $state<string>('');
  /** True when the server is up but rejected our token (e.g. restart with new JWT secret). */
  authError = $state(false);

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
    ws.onAuthError = () => {
      this.authError = true;
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
    this.pinnedTerminals = [];
    this.homeDir = null;
    this.activeApp = 'terminal';
    this.proxyChannels = [];
    this.activeProxyChanID = null;
    this.wallpaper = '';
    this.authError = false;
    this._nextID = 1;
    document.cookie = 'wdd_token=; path=/; max-age=0';
  }

  pinTerminal(chanID: number): void {
    if (!this.pinnedTerminals.includes(chanID)) {
      this.pinnedTerminals = [...this.pinnedTerminals, chanID];
    }
  }

  unpinTerminal(chanID: number): void {
    this.pinnedTerminals = this.pinnedTerminals.filter(id => id !== chanID);
  }

  addPTYChannel(chanID: number, label?: string): void {
    const resolvedLabel = label ?? `Terminal ${chanID}`;
    if (!this.ptyChannels.find((c) => c.chanID === chanID)) {
      this.ptyChannels = [...this.ptyChannels, { chanID, label: resolvedLabel }];
    }
  }

  removePTYChannel(chanID: number): void {
    this.ptyChannels = this.ptyChannels.filter((c) => c.chanID !== chanID);
    this.pinnedTerminals = this.pinnedTerminals.filter(id => id !== chanID);
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
