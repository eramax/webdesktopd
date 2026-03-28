<script lang="ts">
  import type { WSClient } from '$lib/client';
  import { FrameType, decodeJSON } from '$lib/protocol';

  interface Props {
    client: WSClient;
    activeApp: 'terminal' | 'files' | 'proxy';
    onSwitchApp: (app: 'terminal' | 'files' | 'proxy') => void;
  }

  let { client, activeApp, onSwitchApp }: Props = $props();

  // ── Clock ────────────────────────────────────────────────────────────────────

  let now = $state(new Date());
  const clockTimer = setInterval(() => (now = new Date()), 1000);
  $effect(() => () => clearInterval(clockTimer));

  function formatTime(d: Date): string {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
  function formatDate(d: Date): string {
    return d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' });
  }

  // ── Stats ─────────────────────────────────────────────────────────────────────

  interface Stats {
    cpu: number;
    ramUsed: number;
    ramTotal: number;
    diskUsed: number;
    diskTotal: number;
    netRxRate: number;
    netTxRate: number;
    uptime: number;
    loadAvg: number[];
    kernel: string;
    hostname: string;
  }

  let stats = $state<Stats | null>(null);
  let statsOpen = $state(false);

  $effect(() => {
    const _client = client;
    _client.registerBroadcast('dock-stats', (frame) => {
      if (frame.type === FrameType.Stats) {
        try { stats = decodeJSON<Stats>(frame.payload); } catch { /* ignore */ }
      }
    });
    return () => _client.unregisterBroadcast('dock-stats');
  });

  function formatBytes(bytes: number, decimals = 1): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(decimals)) + '\u00a0' + sizes[i];
  }

  function formatUptime(seconds: number): string {
    const d = Math.floor(seconds / 86400);
    const h = Math.floor((seconds % 86400) / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const parts: string[] = [];
    if (d > 0) parts.push(`${d}d`);
    if (h > 0) parts.push(`${h}h`);
    parts.push(`${m}m`);
    return parts.join(' ');
  }

  function cpuBarColor(pct: number): string {
    if (pct >= 80) return 'bg-red-500';
    if (pct >= 50) return 'bg-yellow-500';
    return 'bg-emerald-500';
  }

  function cpuTextColor(pct: number): string {
    if (pct >= 80) return 'text-red-400';
    if (pct >= 50) return 'text-yellow-400';
    return 'text-emerald-400';
  }

  function ramPercent(s: Stats): number {
    return s.ramTotal > 0 ? (s.ramUsed / s.ramTotal) * 100 : 0;
  }
</script>

<!-- Stats popup – appears above the dock tray button -->
{#if statsOpen && stats}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    class="fixed bottom-12 right-2 z-50 w-80 rounded-xl bg-zinc-900 border border-zinc-700 shadow-2xl p-4 text-xs text-zinc-300 space-y-3"
    onclick={(e) => e.stopPropagation()}
  >
    <div class="space-y-1">
      <div class="flex justify-between">
        <span class="text-zinc-500 uppercase tracking-wider text-[10px]">CPU</span>
        <span class="font-mono {cpuTextColor(stats.cpu)}">{stats.cpu.toFixed(1)}%</span>
      </div>
      <div class="h-1.5 bg-zinc-700 rounded-full overflow-hidden">
        <div class="h-full rounded-full transition-all duration-300 {cpuBarColor(stats.cpu)}" style="width: {Math.min(100, stats.cpu).toFixed(1)}%"></div>
      </div>
    </div>
    <div class="space-y-1">
      <div class="flex justify-between">
        <span class="text-zinc-500 uppercase tracking-wider text-[10px]">RAM</span>
        <span class="font-mono">{formatBytes(stats.ramUsed)} / {formatBytes(stats.ramTotal)}</span>
      </div>
      <div class="h-1.5 bg-zinc-700 rounded-full overflow-hidden">
        <div class="h-full rounded-full transition-all duration-300 bg-blue-500" style="width: {ramPercent(stats).toFixed(1)}%"></div>
      </div>
    </div>
    <div class="flex justify-between">
      <span class="text-zinc-500 uppercase tracking-wider text-[10px]">Disk</span>
      <span class="font-mono">{formatBytes(stats.diskUsed)} / {formatBytes(stats.diskTotal)}</span>
    </div>
    <div class="flex justify-between">
      <span class="text-zinc-500 uppercase tracking-wider text-[10px]">Net</span>
      <span class="font-mono">↓{formatBytes(stats.netRxRate)}/s &nbsp;↑{formatBytes(stats.netTxRate)}/s</span>
    </div>
    {#if stats.loadAvg?.length >= 3}
      <div class="flex justify-between">
        <span class="text-zinc-500 uppercase tracking-wider text-[10px]">Load</span>
        <span class="font-mono">{stats.loadAvg[0].toFixed(2)} &nbsp;{stats.loadAvg[1].toFixed(2)} &nbsp;{stats.loadAvg[2].toFixed(2)}</span>
      </div>
    {/if}
    <div class="flex justify-between">
      <span class="text-zinc-500 uppercase tracking-wider text-[10px]">Uptime</span>
      <span class="font-mono">{formatUptime(stats.uptime)}</span>
    </div>
    <div class="border-t border-zinc-700"></div>
    {#if stats.hostname}
      <div class="flex justify-between">
        <span class="text-zinc-500 uppercase tracking-wider text-[10px]">Host</span>
        <span class="font-mono">{stats.hostname}</span>
      </div>
    {/if}
    {#if stats.kernel}
      <div class="flex justify-between">
        <span class="text-zinc-500 uppercase tracking-wider text-[10px]">Kernel</span>
        <span class="font-mono truncate max-w-48 text-right">{stats.kernel}</span>
      </div>
    {/if}
  </div>
{/if}

<!-- Dock bar -->
<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
<div
  class="shrink-0 h-11 bg-zinc-900/95 backdrop-blur border-t border-zinc-800 flex items-center px-2 select-none"
  onclick={() => (statsOpen = false)}
>
  <!-- ── App icons ── -->
  <div class="flex items-center gap-0.5 pr-2 border-r border-zinc-700">
    <!-- Terminal -->
    <button
      onclick={(e) => { e.stopPropagation(); onSwitchApp('terminal'); }}
      title="Terminal"
      class="w-9 h-9 flex items-center justify-center rounded-lg transition
             {activeApp === 'terminal' ? 'bg-blue-600 text-white' : 'text-zinc-400 hover:text-zinc-100 hover:bg-zinc-700'}"
    >
      <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="2" y="3" width="20" height="15" rx="2"/>
        <polyline points="8 10 12 14 8 18"/>
        <line x1="16" y1="18" x2="12" y2="18"/>
      </svg>
    </button>

    <!-- Files -->
    <button
      onclick={(e) => { e.stopPropagation(); onSwitchApp('files'); }}
      title="File Manager"
      class="w-9 h-9 flex items-center justify-center rounded-lg transition
             {activeApp === 'files' ? 'bg-blue-600 text-white' : 'text-zinc-400 hover:text-zinc-100 hover:bg-zinc-700'}"
    >
      <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
      </svg>
    </button>

    <!-- Proxy -->
    <button
      onclick={(e) => { e.stopPropagation(); onSwitchApp('proxy'); }}
      title="Port Proxy"
      class="w-9 h-9 flex items-center justify-center rounded-lg transition
             {activeApp === 'proxy' ? 'bg-blue-600 text-white' : 'text-zinc-400 hover:text-zinc-100 hover:bg-zinc-700'}"
    >
      <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="12" cy="12" r="10"/>
        <line x1="2" y1="12" x2="22" y2="12"/>
        <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
      </svg>
    </button>
  </div>

  <!-- Spacer -->
  <div class="flex-1"></div>

  <!-- ── System tray ── -->
  <div class="flex items-center gap-2 pl-2 border-l border-zinc-700 shrink-0">
    <button
      onclick={(e) => { e.stopPropagation(); statsOpen = !statsOpen; }}
      title="System stats"
      class="flex items-center gap-1.5 h-8 px-2 rounded-lg text-xs transition
             {statsOpen ? 'bg-zinc-700 text-zinc-100' : 'text-zinc-400 hover:bg-zinc-700 hover:text-zinc-200'}"
    >
      {#if stats}
        <span class="font-mono {cpuTextColor(stats.cpu)} tabular-nums">CPU&nbsp;{stats.cpu.toFixed(0)}%</span>
        <span class="text-zinc-600">·</span>
        <span class="font-mono tabular-nums">{formatBytes(stats.ramUsed, 0)}</span>
      {:else}
        <svg class="w-4 h-4 opacity-50" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
        </svg>
      {/if}
    </button>
    <div class="text-right leading-tight px-1">
      <div class="text-xs font-mono text-zinc-300 tabular-nums">{formatTime(now)}</div>
      <div class="text-[10px] text-zinc-600">{formatDate(now)}</div>
    </div>
  </div>
</div>
