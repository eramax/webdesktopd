<script lang="ts">
  import { FrameType } from '$lib/protocol';
  import { decodeJSON } from '$lib/protocol';
  import type { WSClient } from '$lib/client';

  interface Props {
    client: WSClient;
  }

  let { client }: Props = $props();

  interface Stats {
    cpu: number;       // percentage 0-100
    ramUsed: number;   // bytes
    ramTotal: number;  // bytes
    diskUsed: number;  // bytes
    diskTotal: number; // bytes
    netRxRate: number; // bytes/s
    netTxRate: number; // bytes/s
    uptime: number;    // seconds
    loadAvg: number[]; // [1min, 5min, 15min]
    kernel: string;
    hostname: string;
  }

  let stats = $state<Stats | null>(null);

  $effect(() => {
    const _client = client;

    // Stats frames arrive on channel 0 – use broadcast so we don't displace
    // the SessionSync handler registered by the desktop page.
    _client.registerBroadcast('stats-dock', (frame) => {
      if (frame.type === FrameType.Stats) {
        try {
          // Frames are deltas: only changed fields are present.
          // Merge into existing state so static fields (kernel, hostname,
          // ramTotal, diskTotal) are preserved between ticks.
          const delta = decodeJSON<Partial<Stats>>(frame.payload);
          stats = stats ? { ...stats, ...delta } : (delta as Stats);
        } catch {
          // ignore malformed stats
        }
      }
    });

    return () => {
      _client.unregisterBroadcast('stats-dock');
    };
  });

  // ── Formatting helpers ────────────────────────────────────────────────────

  function formatBytes(bytes: number, decimals = 1): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
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

  function cpuColor(pct: number): string {
    if (pct >= 80) return 'bg-red-500';
    if (pct >= 50) return 'bg-yellow-500';
    return 'bg-green-500';
  }

  function cpuTextColor(pct: number): string {
    if (pct >= 80) return 'text-red-400';
    if (pct >= 50) return 'text-yellow-400';
    return 'text-green-400';
  }
</script>

<footer class="shrink-0 bg-zinc-900 border-t border-zinc-800 px-4 py-1.5 text-xs text-zinc-400 flex items-center gap-5 overflow-x-auto">
  {#if stats}
    <!-- CPU -->
    <div class="flex items-center gap-2 shrink-0">
      <span class="text-zinc-500">CPU</span>
      <div class="w-20 h-1.5 bg-zinc-700 rounded-full overflow-hidden">
        <div
          class="h-full rounded-full transition-all duration-300 {cpuColor(stats.cpu)}"
          style="width: {Math.min(100, stats.cpu).toFixed(1)}%"
        ></div>
      </div>
      <span class="{cpuTextColor(stats.cpu)} tabular-nums font-mono">
        {stats.cpu.toFixed(1)}%
      </span>
    </div>

    <!-- RAM -->
    <div class="flex items-center gap-1.5 shrink-0">
      <span class="text-zinc-500">RAM</span>
      <span class="font-mono tabular-nums">
        {formatBytes(stats.ramUsed)} / {formatBytes(stats.ramTotal)}
      </span>
    </div>

    <!-- Disk -->
    <div class="flex items-center gap-1.5 shrink-0">
      <span class="text-zinc-500">Disk</span>
      <span class="font-mono tabular-nums">
        {formatBytes(stats.diskUsed)} / {formatBytes(stats.diskTotal)}
      </span>
    </div>

    <!-- Network -->
    <div class="flex items-center gap-1.5 shrink-0">
      <span class="text-zinc-500">Net</span>
      <span class="font-mono tabular-nums">
        ↓{formatBytes(stats.netRxRate)}/s ↑{formatBytes(stats.netTxRate)}/s
      </span>
    </div>

    <!-- Load average -->
    {#if stats.loadAvg && stats.loadAvg.length >= 3}
      <div class="flex items-center gap-1.5 shrink-0">
        <span class="text-zinc-500">Load</span>
        <span class="font-mono tabular-nums">
          {stats.loadAvg[0].toFixed(2)} {stats.loadAvg[1].toFixed(2)} {stats.loadAvg[2].toFixed(2)}
        </span>
      </div>
    {/if}

    <!-- Uptime -->
    <div class="flex items-center gap-1.5 shrink-0">
      <span class="text-zinc-500">Up</span>
      <span class="font-mono tabular-nums">{formatUptime(stats.uptime)}</span>
    </div>

    <!-- Hostname / kernel (pushed right) -->
    <div class="ml-auto flex items-center gap-3 shrink-0 text-zinc-600">
      {#if stats.hostname}
        <span>{stats.hostname}</span>
      {/if}
      {#if stats.kernel}
        <span class="hidden sm:inline">{stats.kernel}</span>
      {/if}
    </div>
  {:else}
    <span class="text-zinc-600 italic">Waiting for stats...</span>
  {/if}
</footer>
