import { WSClient } from './client';

export interface PTYChannel {
  chanID: number;
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
  /** Which app occupies the main area: 'terminal' or 'files'. */
  activeApp = $state<'terminal' | 'files'>('terminal');
  /** Whether the file manager is open. */
  fileManagerOpen = $state(false);

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
    this._nextID = 1;
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

  addPTYChannel(chanID: number, label?: string): void {
    const resolvedLabel = label ?? `Terminal ${chanID}`;
    // Avoid duplicates
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

  /** Returns a new unique channel ID (increments monotonically). */
  nextChannelID(): number {
    // Channels start at 1; channel 0 is reserved for broadcast/stats.
    const id = this._nextID;
    this._nextID++;
    return id;
  }
}

export const session = new SessionStore();
