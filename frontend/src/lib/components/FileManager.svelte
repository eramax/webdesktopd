<script lang="ts">
  import type { WSClient } from '$lib/client';
  import { FrameType, decodeJSON } from '$lib/protocol';

  interface Props {
    client: WSClient;
    homeDir: string;
  }

  let { client, homeDir }: Props = $props();

  // ── Types ─────────────────────────────────────────────────────────────────

  interface FileInfo {
    name: string;
    size: number;
    isDir: boolean;
    mode: string;
    modTime: string;
  }

  interface FileListResponse {
    path: string;
    entries?: FileInfo[];
    error?: string;
  }

  interface ClipboardEntry {
    op: 'copy' | 'cut';
    paths: string[];
  }

  interface Toast {
    id: string;
    kind: 'op' | 'upload';
    label: string;
    progress?: number; // 0-100
    done?: boolean;
    error?: string;
  }

  type IconKind = 'folder' | 'image' | 'video' | 'audio' | 'archive' | 'code' | 'pdf' | 'file';

  // ── Panel state ───────────────────────────────────────────────────────────

  interface PanelSnapshot {
    id: number;
    label: string;
    path: string;
    entries: FileInfo[];
  }

  let _panelCounter = $state(0);
  let panels = $state<PanelSnapshot[]>([]);
  let activePanelId = $state(0);
  let pinnedPanelIds = $state<number[]>([]);
  let renamingPanelId = $state<number | null>(null);
  let renamePanelValue = $state('');

  let pinnedPanels = $derived(panels.filter(p => pinnedPanelIds.includes(p.id)));
  let unpinnedPanels = $derived(panels.filter(p => !pinnedPanelIds.includes(p.id)));

  function labelFromPath(p: string): string {
    const seg = p.replace(/\/$/, '').split('/').filter(Boolean).pop();
    return seg ?? '/';
  }

  function addPanel(path: string) {
    // Save current panel state first
    saveCurrent();
    const id = ++_panelCounter;
    panels = [...panels, { id, label: labelFromPath(path), path, entries: [] }];
    activePanelId = id;
    currentPath = path;
    entries = [];
    selected = new Set();
    lastSelected = null;
    requestList(path);
  }

  function switchToPanel(id: number) {
    if (id === activePanelId) return;
    saveCurrent();
    activePanelId = id;
    const target = panels.find(p => p.id === id);
    if (!target) return;
    currentPath = target.path;
    entries = target.entries;
    selected = new Set();
    lastSelected = null;
    renamingName = null;
    contextMenu = null;
    requestList(currentPath);
  }

  function closePanel(id: number) {
    if (panels.length <= 1) return;
    const idx = panels.findIndex(p => p.id === id);
    panels = panels.filter(p => p.id !== id);
    pinnedPanelIds = pinnedPanelIds.filter(pid => pid !== id);
    if (activePanelId === id) {
      const next = panels[Math.min(idx, panels.length - 1)];
      if (next) {
        activePanelId = next.id;
        currentPath = next.path;
        entries = next.entries;
        selected = new Set();
        lastSelected = null;
        requestList(currentPath);
      }
    }
  }

  function saveCurrent() {
    const idx = panels.findIndex(p => p.id === activePanelId);
    if (idx >= 0) {
      panels[idx] = { ...panels[idx], path: currentPath, entries: [...entries] };
    }
  }

  function pinPanel(id: number) {
    if (!pinnedPanelIds.includes(id)) {
      pinnedPanelIds = [...pinnedPanelIds, id];
    }
  }

  function unpinPanel(id: number) {
    pinnedPanelIds = pinnedPanelIds.filter(pid => pid !== id);
  }

  function startRenamePanel(id: number, label: string) {
    renamingPanelId = id;
    renamePanelValue = label;
  }

  function commitRenamePanel() {
    if (renamingPanelId !== null && renamePanelValue.trim()) {
      const idx = panels.findIndex(p => p.id === renamingPanelId);
      if (idx >= 0) panels[idx].label = renamePanelValue.trim();
    }
    renamingPanelId = null;
  }

  // ── State ─────────────────────────────────────────────────────────────────

  let currentPath = $state('/');
  let entries = $state<FileInfo[]>([]);
  let loading = $state(false);
  let errorMsg = $state<string | null>(null);
  let showHidden = $state(false);
  let selected = $state<Set<string>>(new Set());
  let clipboard = $state<ClipboardEntry | null>(null);
  let renamingName = $state<string | null>(null);
  let renameValue = $state('');
  let contextMenu = $state<{ x: number; y: number; name: string } | null>(null);
  let dragOver = $state(false);
  let lastSelected = $state<string | null>(null);

  let uploadInput = $state<HTMLInputElement | undefined>(undefined);
  let uploadFolderInput = $state<HTMLInputElement | undefined>(undefined);
  let toasts = $state<Toast[]>([]);

  // ── Toast helpers ─────────────────────────────────────────────────────────

  let _toastCounter = 0;
  // uploadID (uuid) → toastID
  const uploadToasts = new Map<string, string>();
  // uploadID (uuid) → total file bytes (for progress %)
  const uploadSizes = new Map<string, number>();

  function addToast(t: Omit<Toast, 'id'>): string {
    const id = String(++_toastCounter);
    toasts = [...toasts, { ...t, id }];
    return id;
  }

  function updateToast(id: string, patch: Partial<Omit<Toast, 'id'>>) {
    toasts = toasts.map(t => t.id === id ? { ...t, ...patch } : t);
  }

  function dismissToast(id: string) {
    toasts = toasts.filter(t => t.id !== id);
  }

  function autoDismiss(id: string, ms = 2000) {
    setTimeout(() => dismissToast(id), ms);
  }

  function opToast(label: string) {
    const id = addToast({ kind: 'op', label });
    autoDismiss(id, 2000);
  }

  // download id → accumulation state
  interface DownloadState {
    name: string;
    total: number;
    received: number;
    chunks: Map<number, Uint8Array>;
  }
  const downloads = new Map<string, DownloadState>();

  // ── Pure helpers ──────────────────────────────────────────────────────────

  function join(...parts: string[]): string {
    return parts.join('/').replace(/\/+/g, '/') || '/';
  }

  function parent(p: string): string {
    if (p === '/') return '/';
    const parts = p.replace(/\/$/, '').split('/');
    parts.pop();
    return parts.join('/') || '/';
  }

  function abs(name: string): string {
    return join(currentPath, name);
  }

  function visible(f: FileInfo): boolean {
    return showHidden || !f.name.startsWith('.');
  }

  function sortedList(list: FileInfo[]): FileInfo[] {
    return [...list]
      .filter(visible)
      .sort((a, b) => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        return a.name.localeCompare(b.name);
      });
  }

  function fmtSize(bytes: number): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return (bytes / Math.pow(k, i)).toFixed(1) + '\u00a0' + units[i];
  }

  function fmtDate(iso: string): string {
    try {
      return new Date(iso).toLocaleString([], {
        month: 'short', day: 'numeric', year: 'numeric',
        hour: '2-digit', minute: '2-digit',
      });
    } catch { return iso; }
  }

  function iconKind(f: FileInfo): IconKind {
    if (f.isDir) return 'folder';
    const ext = f.name.split('.').pop()?.toLowerCase() ?? '';
    if (['jpg','jpeg','png','gif','webp','svg','bmp','ico','avif'].includes(ext)) return 'image';
    if (['mp4','mkv','avi','mov','webm','flv','wmv'].includes(ext)) return 'video';
    if (['mp3','ogg','flac','wav','aac','m4a','opus'].includes(ext)) return 'audio';
    if (['zip','tar','gz','bz2','xz','7z','rar','zst'].includes(ext)) return 'archive';
    if (['js','ts','jsx','tsx','py','go','rs','c','cpp','h','java','sh','bash',
         'json','yaml','yml','toml','xml','html','css','scss','sql','md','svelte'].includes(ext)) return 'code';
    if (ext === 'pdf') return 'pdf';
    return 'file';
  }

  // ── Derived ───────────────────────────────────────────────────────────────

  let display = $derived(sortedList(entries));

  let breadcrumbs = $derived.by(() => {
    const parts = currentPath.split('/').filter(Boolean);
    const result: { label: string; path: string }[] = [{ label: '/', path: '/' }];
    let acc = '';
    for (const p of parts) {
      acc += '/' + p;
      result.push({ label: p, path: acc });
    }
    return result;
  });

  // ── Network ───────────────────────────────────────────────────────────────

  function requestList(path: string) {
    loading = true;
    errorMsg = null;
    client.send({ type: FrameType.FileList, chanID: 0, payload: new TextEncoder().encode(path) });
  }

  function navigate(path: string) {
    selected = new Set();
    renamingName = null;
    contextMenu = null;
    currentPath = path;
    // Auto-update active panel label and path
    const idx = panels.findIndex(p => p.id === activePanelId);
    if (idx >= 0) {
      panels[idx].path = path;
      panels[idx].label = labelFromPath(path);
    }
    requestList(path);
  }

  function fileOp(op: string, path: string, dst?: string) {
    client.sendJSON(FrameType.FileOp, 0, { op, path, dst: dst ?? '', mode: 0 });
  }

  // ── Actions ───────────────────────────────────────────────────────────────

  function createFolder() {
    const name = prompt('Folder name:');
    if (!name?.trim()) return;
    fileOp('mkdir', join(currentPath, name.trim()));
    opToast(`Created folder "${name.trim()}"`);
    setTimeout(() => requestList(currentPath), 200);
  }

  function createFile() {
    const name = prompt('File name:');
    if (!name?.trim()) return;
    fileOp('touch', join(currentPath, name.trim()));
    opToast(`Created file "${name.trim()}"`);
    setTimeout(() => requestList(currentPath), 200);
  }

  function deleteSelected() {
    if (selected.size === 0) return;
    const names = [...selected].join(', ');
    if (!confirm(`Delete ${selected.size} item(s)?\n${names}`)) return;
    for (const name of selected) fileOp('delete', abs(name));
    opToast(`Deleted ${selected.size} item${selected.size !== 1 ? 's' : ''}`);
    selected = new Set();
    setTimeout(() => requestList(currentPath), 200);
  }

  function copySelected() {
    if (selected.size === 0) return;
    clipboard = { op: 'copy', paths: [...selected].map(abs) };
  }

  function cutSelected() {
    if (selected.size === 0) return;
    clipboard = { op: 'cut', paths: [...selected].map(abs) };
  }

  function paste() {
    if (!clipboard) return;
    const n = clipboard.paths.length;
    const verb = clipboard.op === 'copy' ? 'Copied' : 'Moved';
    for (const srcPath of clipboard.paths) {
      const name = srcPath.split('/').pop() ?? '';
      const dst = join(currentPath, name);
      fileOp(clipboard.op === 'copy' ? 'copy' : 'rename', srcPath, dst);
    }
    opToast(`${verb} ${n} item${n !== 1 ? 's' : ''}`);
    if (clipboard.op === 'cut') clipboard = null;
    setTimeout(() => requestList(currentPath), 300);
  }

  function commitRename() {
    if (!renamingName || !renameValue.trim() || renameValue === renamingName) {
      renamingName = null;
      return;
    }
    fileOp('rename', abs(renamingName), abs(renameValue.trim()));
    opToast(`Renamed "${renamingName}" → "${renameValue.trim()}"`);
    renamingName = null;
    setTimeout(() => requestList(currentPath), 200);
  }

  function downloadSelected() {
    for (const name of selected) {
      const entry = entries.find(e => e.name === name);
      if (!entry || entry.isDir) continue;
      const id = crypto.randomUUID();
      downloads.set(id, { name, total: 0, received: 0, chunks: new Map() });
      client.sendJSON(FrameType.FileDownloadReq, 0, { id, path: abs(name) });
    }
  }

  // ── Upload ────────────────────────────────────────────────────────────────

  async function uploadSingleFile(file: File, destPath: string) {
    // uploadID must be exactly 36 bytes (UUID is exactly 36 chars)
    const rawID = crypto.randomUUID();
    const idBytes = new TextEncoder().encode(rawID);
    const tid = addToast({ kind: 'upload', label: file.name, progress: 0 });
    uploadToasts.set(rawID, tid);

    const data = new Uint8Array(await file.arrayBuffer());
    uploadSizes.set(rawID, Math.max(data.length, 1));

    const pathBytes = new TextEncoder().encode(destPath);
    const chunkSize = 64 * 1024;
    let offset = 0;

    do {
      const chunk = data.slice(offset, offset + chunkSize);
      // Wire format: uploadID(36) | pathLen(2 BE) | path | offset(8 BE) | data
      const payload = new Uint8Array(36 + 2 + pathBytes.length + 8 + chunk.length);
      const dv = new DataView(payload.buffer);
      payload.set(idBytes, 0);
      dv.setUint16(36, pathBytes.length, false);
      payload.set(pathBytes, 38);
      dv.setUint32(38 + pathBytes.length, Math.floor(offset / 0x100000000), false);
      dv.setUint32(38 + pathBytes.length + 4, offset >>> 0, false);
      payload.set(chunk, 38 + pathBytes.length + 8);
      client.send({ type: FrameType.FileUpload, chanID: 0, payload });
      offset += chunk.length;
      if (chunk.length > 0) await new Promise(r => setTimeout(r, 0));
    } while (offset < data.length);
  }

  async function uploadFiles(files: FileList | File[], useRelativePath = false) {
    const arr = Array.from(files);
    await Promise.all(arr.map(file => {
      const destPath = (useRelativePath && file.webkitRelativePath)
        ? join(currentPath, file.webkitRelativePath)
        : join(currentPath, file.name);
      return uploadSingleFile(file, destPath);
    }));
    setTimeout(() => requestList(currentPath), 400);
  }

  // ── Selection ─────────────────────────────────────────────────────────────

  function handleClick(e: MouseEvent, name: string) {
    if (renamingName) { commitRename(); return; }
    contextMenu = null;

    if (e.ctrlKey || e.metaKey) {
      const next = new Set(selected);
      next.has(name) ? next.delete(name) : next.add(name);
      selected = next;
      lastSelected = name;
    } else if (e.shiftKey && lastSelected) {
      const names = display.map(f => f.name);
      const a = names.indexOf(lastSelected);
      const b = names.indexOf(name);
      const [lo, hi] = a < b ? [a, b] : [b, a];
      selected = new Set(names.slice(lo, hi + 1));
    } else {
      selected = new Set([name]);
      lastSelected = name;
    }
  }

  function handleDblClick(f: FileInfo) {
    if (renamingName) { commitRename(); return; }
    if (f.isDir) navigate(abs(f.name));
  }

  function handleContextMenu(e: MouseEvent, name: string) {
    e.preventDefault();
    if (!selected.has(name)) { selected = new Set([name]); lastSelected = name; }
    contextMenu = { x: e.clientX, y: e.clientY, name };
  }

  // ── Drag-and-drop ─────────────────────────────────────────────────────────

  function onDragOver(e: DragEvent) { e.preventDefault(); dragOver = true; }
  function onDragLeave() { dragOver = false; }
  function onDrop(e: DragEvent) {
    e.preventDefault();
    dragOver = false;
    if (e.dataTransfer?.files.length) uploadFiles(e.dataTransfer.files);
  }

  // ── Keyboard ──────────────────────────────────────────────────────────────

  function onKeyDown(e: KeyboardEvent) {
    if (renamingName) return;
    const mod = e.ctrlKey || e.metaKey;
    if (e.key === 'F2' && selected.size === 1) { startRename([...selected][0]); e.preventDefault(); }
    else if (e.key === 'Delete') { deleteSelected(); e.preventDefault(); }
    else if (mod && e.key === 'c') { copySelected(); e.preventDefault(); }
    else if (mod && e.key === 'x') { cutSelected(); e.preventDefault(); }
    else if (mod && e.key === 'v') { paste(); e.preventDefault(); }
    else if (e.key === 'Backspace') { navigate(parent(currentPath)); e.preventDefault(); }
    else if (e.key === 'Escape') { selected = new Set(); contextMenu = null; }
  }

  function startRename(name: string) {
    renamingName = name;
    renameValue = name;
    contextMenu = null;
  }

  // ── Svelte action: auto-select input content on mount ─────────────────────
  function selectAll(node: HTMLInputElement) {
    setTimeout(() => node.select(), 10);
    return {};
  }

  // ── Broadcast listener ────────────────────────────────────────────────────

  $effect(() => {
    const _client = client;

    _client.registerBroadcast('file-manager', (frame) => {
      if (frame.type === FrameType.FileListResp) {
        loading = false;
        try {
          const resp = decodeJSON<FileListResponse>(frame.payload);
          if (resp.path === currentPath) {
            errorMsg = resp.error ?? null;
            entries = resp.error ? [] : (resp.entries ?? []);
          }
        } catch { /* ignore */ }
      }

      if (frame.type === FrameType.FileDownload) {
        // [downloadID(36) | offset(8 BE) | data...]
        if (frame.payload.length < 44) return;
        const id = new TextDecoder().decode(frame.payload.slice(0, 36)).trimEnd();
        const dl = downloads.get(id);
        if (!dl) return;
        const dv = new DataView(frame.payload.buffer, frame.payload.byteOffset);
        const offset = dv.getUint32(36, false) * 0x100000000 + dv.getUint32(40, false);
        dl.chunks.set(offset, frame.payload.slice(44));
        dl.received += frame.payload.length - 44;
      }

      if (frame.type === FrameType.Progress) {
        try {
          const p = decodeJSON<{ id: string; bytesSent: number; total: number; error?: string }>(frame.payload);

          // Upload progress toast
          const uploadID = p.id.trim();
          const tid = uploadToasts.get(uploadID);
          if (tid) {
            if (p.error) {
              updateToast(tid, { error: p.error, done: true });
              uploadToasts.delete(uploadID);
              uploadSizes.delete(uploadID);
              autoDismiss(tid, 3000);
            } else {
              const total = uploadSizes.get(uploadID) ?? 1;
              const pct = Math.min(100, Math.round(p.bytesSent / total * 100));
              updateToast(tid, { progress: pct });
              if (p.bytesSent >= total) {
                updateToast(tid, { done: true, progress: 100 });
                uploadToasts.delete(uploadID);
                uploadSizes.delete(uploadID);
                autoDismiss(tid, 1500);
              }
            }
          }

          const dl = downloads.get(p.id);
          if (!dl) return;
          if (p.error) { downloads.delete(p.id); return; }
          dl.total = p.total;
          if (p.total > 0 && p.bytesSent >= p.total) {
            // Assemble and trigger browser download
            const sorted = [...dl.chunks.entries()].sort((a, b) => a[0] - b[0]);
            const totalBytes = sorted.reduce((s, [, c]) => s + c.length, 0);
            const out = new Uint8Array(totalBytes);
            let pos = 0;
            for (const [, c] of sorted) { out.set(c, pos); pos += c.length; }
            const url = URL.createObjectURL(new Blob([out]));
            const a = document.createElement('a');
            a.href = url; a.download = dl.name; a.click();
            URL.revokeObjectURL(url);
            downloads.delete(p.id);
          }
        } catch { /* ignore */ }
      }
    });

    // Initialize first panel
    const startPath = homeDir || '/';
    const initId = 1;
    _panelCounter = 1;
    panels = [{ id: initId, label: labelFromPath(startPath), path: startPath, entries: [] }];
    activePanelId = initId;
    currentPath = startPath;
    entries = [];
    requestList(startPath);

    return () => _client.unregisterBroadcast('file-manager');
  });
</script>

<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions a11y_no_noninteractive_element_interactions -->
<div
  class="relative flex h-full bg-zinc-950 text-zinc-200 text-sm select-none outline-none"
  tabindex="-1"
  role="region"
  aria-label="File Manager"
  onkeydown={onKeyDown}
  onclick={() => { selected = new Set(); contextMenu = null; }}
>

  <!-- ── Left sidebar ── -->
  <aside class="w-44 shrink-0 flex flex-col bg-zinc-900 border-r border-zinc-800 select-none" onclick={(e) => e.stopPropagation()}>

    {#if pinnedPanels.length > 0}
      <div class="px-3 pt-2.5 pb-1">
        <span class="text-[10px] uppercase tracking-wider text-zinc-600 font-semibold">Pinned</span>
      </div>
      {#each pinnedPanels as panel (panel.id)}
        {@render panelItem(panel, true)}
      {/each}
      <div class="mx-2 my-1.5 border-t border-zinc-800"></div>
    {/if}

    <div class="flex-1 overflow-y-auto py-1">
      {#if unpinnedPanels.length > 0}
        {#if pinnedPanels.length > 0}
          <div class="px-3 pb-1">
            <span class="text-[10px] uppercase tracking-wider text-zinc-600 font-semibold">Panels</span>
          </div>
        {/if}
        {#each unpinnedPanels as panel (panel.id)}
          {@render panelItem(panel, false)}
        {/each}
      {:else if pinnedPanels.length === 0}
        <p class="px-3 py-4 text-xs text-zinc-600 text-center">No panels</p>
      {/if}
    </div>

    <div class="p-2 border-t border-zinc-800">
      <button
        onclick={() => addPanel(currentPath)}
        class="w-full flex items-center gap-1.5 px-2 py-1.5 rounded text-xs text-zinc-500 hover:text-zinc-200 hover:bg-zinc-800 transition"
      >
        <svg class="w-3.5 h-3.5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        New Panel
      </button>
    </div>
  </aside>

  <!-- ── Right: toolbar + file listing ── -->
  <div class="flex-1 flex flex-col min-w-0 relative">

  <!-- ── Toolbar ── -->
  <div class="flex items-center gap-1 px-2 py-1.5 bg-zinc-900 border-b border-zinc-800 flex-wrap shrink-0">

    <!-- Navigation -->
    <button onclick={(e) => { e.stopPropagation(); navigate(parent(currentPath)); }}
      title="Up" class="w-7 h-7 flex items-center justify-center rounded text-zinc-500 hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed" disabled={currentPath === '/'}>
      {@render iconUp()}
    </button>
    <button onclick={(e) => { e.stopPropagation(); navigate(homeDir || '/'); }}
      title="Home" class="w-7 h-7 flex items-center justify-center rounded text-zinc-500 hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
      {@render iconHome()}
    </button>
    <button onclick={(e) => { e.stopPropagation(); requestList(currentPath); }}
      title="Refresh" class="w-7 h-7 flex items-center justify-center rounded text-zinc-500 hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
      {@render iconRefresh()}
    </button>

    <div class="w-px h-5 bg-zinc-700 mx-0.5"></div>

    <!-- Breadcrumbs -->
    <div class="flex items-center gap-0.5 flex-1 min-w-0 overflow-x-auto text-xs">
      {#each breadcrumbs as crumb, i}
        {#if i > 0}<span class="text-zinc-600 mx-0.5">›</span>{/if}
        <button
          onclick={(e) => { e.stopPropagation(); navigate(crumb.path); }}
          class="px-1 py-0.5 rounded hover:bg-zinc-700 hover:text-white transition shrink-0
                 {i === breadcrumbs.length - 1 ? 'text-zinc-100 font-medium' : 'text-zinc-500'}"
        >{crumb.label}</button>
      {/each}
    </div>

    <div class="w-px h-5 bg-zinc-700 mx-0.5"></div>

    <!-- Actions -->
    <button onclick={(e) => { e.stopPropagation(); createFolder(); }} title="New Folder" class="flex items-center gap-1 px-2 h-7 rounded text-zinc-400 text-xs hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
      {@render iconFolderPlus()} <span>Folder</span>
    </button>
    <button onclick={(e) => { e.stopPropagation(); createFile(); }} title="New File" class="flex items-center gap-1 px-2 h-7 rounded text-zinc-400 text-xs hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
      {@render iconFilePlus()} <span>File</span>
    </button>
    <button onclick={(e) => { e.stopPropagation(); uploadInput?.click(); }} title="Upload files" class="flex items-center gap-1 px-2 h-7 rounded text-zinc-400 text-xs hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
      {@render iconUpload()} <span>Upload</span>
    </button>
    <button onclick={(e) => { e.stopPropagation(); uploadFolderInput?.click(); }} title="Upload folder" class="flex items-center gap-1 px-2 h-7 rounded text-zinc-400 text-xs hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
      {@render iconFolderUpload()} <span>Dir</span>
    </button>
    <input
      type="file" multiple class="hidden"
      bind:this={uploadInput}
      onchange={(e) => { if (e.currentTarget.files) uploadFiles(e.currentTarget.files, false); e.currentTarget.value=''; }}
    />
    <input
      type="file" multiple class="hidden"
      bind:this={uploadFolderInput}
      webkitdirectory
      onchange={(e) => { if (e.currentTarget.files) uploadFiles(e.currentTarget.files, true); e.currentTarget.value=''; }}
    />

    {#if selected.size > 0}
      <div class="w-px h-5 bg-zinc-700 mx-0.5"></div>
      <button onclick={(e) => { e.stopPropagation(); downloadSelected(); }}
        title="Download" class="flex items-center gap-1 px-2 h-7 rounded text-zinc-400 text-xs hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed"
        disabled={[...selected].every(n => entries.find(e => e.name === n)?.isDir)}>
        {@render iconDownload()} <span>Download</span>
      </button>
      <button onclick={(e) => { e.stopPropagation(); copySelected(); }} title="Copy (Ctrl+C)" class="flex items-center gap-1 px-2 h-7 rounded text-zinc-400 text-xs hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
        {@render iconCopy()} <span>Copy</span>
      </button>
      <button onclick={(e) => { e.stopPropagation(); cutSelected(); }} title="Cut (Ctrl+X)" class="flex items-center gap-1 px-2 h-7 rounded text-zinc-400 text-xs hover:text-zinc-100 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
        {@render iconCut()} <span>Cut</span>
      </button>
      <button onclick={(e) => { e.stopPropagation(); deleteSelected(); }}
        title="Delete (Del)" class="flex items-center gap-1 px-2 h-7 rounded text-red-400 text-xs hover:text-red-300 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
        {@render iconTrash()} <span>Delete</span>
      </button>
    {/if}

    {#if clipboard}
      <button onclick={(e) => { e.stopPropagation(); paste(); }}
        title="Paste (Ctrl+V)" class="flex items-center gap-1 px-2 h-7 rounded text-blue-400 text-xs hover:text-blue-300 hover:bg-zinc-700 transition disabled:opacity-30 disabled:cursor-not-allowed">
        {@render iconPaste()} <span>Paste{clipboard.op === 'cut' ? ' (move)' : ''}</span>
      </button>
    {/if}

    <div class="w-px h-5 bg-zinc-700 mx-0.5"></div>

    <button
      onclick={(e) => { e.stopPropagation(); showHidden = !showHidden; }}
      title="Toggle hidden files"
      class="flex items-center gap-1 px-2 h-7 rounded text-xs hover:text-zinc-100 hover:bg-zinc-700 transition {showHidden ? 'text-blue-400' : 'text-zinc-400'}"
    >
      {@render iconEye()} <span>Hidden</span>
    </button>
  </div>

  <!-- ── File grid ── -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="flex-1 overflow-auto p-3 transition-shadow"
    class:ring-2={dragOver}
    class:ring-inset={dragOver}
    class:ring-blue-500={dragOver}
    ondragover={onDragOver}
    ondragleave={onDragLeave}
    ondrop={onDrop}
    onclick={(e) => { if (e.target === e.currentTarget) { selected = new Set(); contextMenu = null; } }}
  >
    {#if loading}
      <div class="flex items-center justify-center h-32 text-zinc-600 gap-2">
        <svg class="w-4 h-4 animate-spin" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <path d="M21 12a9 9 0 1 1-6.219-8.56"/>
        </svg>
        Loading…
      </div>
    {:else if errorMsg}
      <div class="flex items-center justify-center h-32 text-red-400 gap-2 text-sm">
        <svg class="w-5 h-5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
        {errorMsg}
      </div>
    {:else if display.length === 0}
      <div class="flex flex-col items-center justify-center h-32 text-zinc-600 gap-2 text-sm">
        <svg class="w-8 h-8" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
        </svg>
        Empty{!showHidden ? ' (hidden files may exist)' : ''}
      </div>
    {:else}
      <div class="grid gap-1" style="grid-template-columns: repeat(auto-fill, minmax(90px,1fr))">
        {#each display as f (f.name)}
          <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
          <div
            class="flex flex-col items-center gap-1 p-2 rounded-lg cursor-pointer transition
                   {selected.has(f.name)
                     ? 'bg-blue-600/40 ring-1 ring-blue-500'
                     : clipboard?.op === 'cut' && clipboard.paths.includes(abs(f.name))
                       ? 'opacity-40 hover:bg-zinc-800/60'
                       : 'hover:bg-zinc-800/80'}"
            onclick={(e) => { e.stopPropagation(); handleClick(e, f.name); }}
            ondblclick={() => handleDblClick(f)}
            oncontextmenu={(e) => handleContextMenu(e, f.name)}
            title="{f.name}{f.isDir ? '' : ' · ' + fmtSize(f.size)} · {fmtDate(f.modTime)}"
          >
            {@render fileIcon(iconKind(f))}

            {#if renamingName === f.name}
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <input
                type="text"
                class="w-full text-center text-xs bg-zinc-800 border border-blue-500 rounded px-1 py-0.5 outline-none"
                bind:value={renameValue}
                use:selectAll
                onclick={(e) => e.stopPropagation()}
                onkeydown={(e) => {
                  e.stopPropagation();
                  if (e.key === 'Enter') commitRename();
                  if (e.key === 'Escape') renamingName = null;
                }}
              />
            {:else}
              <span class="text-xs text-center break-all leading-tight line-clamp-2 max-w-full
                           {f.name.startsWith('.') ? 'text-zinc-500' : 'text-zinc-300'}">
                {f.name}
              </span>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>

  <!-- ── Status bar ── -->
  <div class="flex items-center justify-between px-3 py-1 bg-zinc-900 border-t border-zinc-800 text-xs text-zinc-600 shrink-0 gap-4">
    <span>
      {display.length} item{display.length !== 1 ? 's' : ''}
      {#if selected.size > 0}&nbsp;·&nbsp;{selected.size} selected{/if}
    </span>
    {#if clipboard}
      <span class="text-blue-500 shrink-0">
        {clipboard.paths.length} in clipboard ({clipboard.op})
      </span>
    {/if}
    <span class="truncate text-right font-mono">{currentPath}</span>
  </div>

  <!-- ── Context menu ── -->
  {#if contextMenu}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions a11y_no_noninteractive_element_interactions -->
    <div
      class="fixed z-50 bg-zinc-800 border border-zinc-700 rounded-lg shadow-2xl py-1 min-w-40 text-sm"
      style="left:{contextMenu.x}px; top:{contextMenu.y}px"
      onclick={(e) => e.stopPropagation()}
    >
      {#if selected.size === 1}
        <button class="block w-full text-left px-4 py-1.5 text-zinc-300 text-sm hover:bg-zinc-700 hover:text-zinc-100 transition disabled:opacity-40 disabled:cursor-not-allowed" onclick={() => { const f = entries.find(e=>e.name===contextMenu!.name); if(f) handleDblClick(f); contextMenu=null; }}>
          Open
        </button>
        <button class="block w-full text-left px-4 py-1.5 text-zinc-300 text-sm hover:bg-zinc-700 hover:text-zinc-100 transition disabled:opacity-40 disabled:cursor-not-allowed" onclick={() => startRename(contextMenu!.name)}>Rename (F2)</button>
        <hr class="border-zinc-700 my-1" />
      {/if}
      <button class="block w-full text-left px-4 py-1.5 text-zinc-300 text-sm hover:bg-zinc-700 hover:text-zinc-100 transition disabled:opacity-40 disabled:cursor-not-allowed" onclick={() => { copySelected(); contextMenu=null; }}>Copy</button>
      <button class="block w-full text-left px-4 py-1.5 text-zinc-300 text-sm hover:bg-zinc-700 hover:text-zinc-100 transition disabled:opacity-40 disabled:cursor-not-allowed" onclick={() => { cutSelected(); contextMenu=null; }}>Cut</button>
      {#if clipboard}
        <button class="block w-full text-left px-4 py-1.5 text-blue-400 text-sm hover:bg-zinc-700 hover:text-blue-300 transition" onclick={() => { paste(); contextMenu=null; }}>Paste</button>
      {/if}
      <hr class="border-zinc-700 my-1" />
      <button class="block w-full text-left px-4 py-1.5 text-zinc-300 text-sm hover:bg-zinc-700 hover:text-zinc-100 transition disabled:opacity-40 disabled:cursor-not-allowed" onclick={() => { downloadSelected(); contextMenu=null; }}
        disabled={[...selected].every(n => entries.find(e=>e.name===n)?.isDir)}>
        Download
      </button>
      <hr class="border-zinc-700 my-1" />
      <button class="block w-full text-left px-4 py-1.5 text-red-400 text-sm hover:bg-zinc-700 hover:text-red-300 transition" onclick={() => { deleteSelected(); contextMenu=null; }}>Delete</button>
    </div>
  {/if}

  <!-- ── Toast overlay ── -->
  {#if toasts.length > 0}
    <div class="absolute bottom-10 right-3 z-50 flex flex-col gap-1.5 pointer-events-none" style="max-width:280px">
      {#each toasts as t (t.id)}
        <div class="flex items-center gap-2 px-3 py-2 rounded-lg shadow-xl text-xs
                    {t.error ? 'bg-red-900/90 border border-red-700' : t.done ? 'bg-zinc-700/90 border border-zinc-600' : 'bg-zinc-800/95 border border-zinc-700'}">
          {#if t.kind === 'upload' && !t.done && !t.error}
            <!-- circular progress ring with % in centre -->
            <svg class="w-6 h-6 shrink-0" viewBox="0 0 24 24" fill="none" style="transform:rotate(-90deg)">
              <circle cx="12" cy="12" r="9" stroke="#3f3f46" stroke-width="2.5"/>
              <circle cx="12" cy="12" r="9" stroke="#60a5fa" stroke-width="2.5"
                stroke-linecap="round"
                stroke-dasharray="56.55"
                stroke-dashoffset="{56.55 * (1 - (t.progress ?? 0) / 100)}"/>
              <text x="12" y="12" text-anchor="middle" dominant-baseline="central"
                font-size="5" fill="white" stroke="none"
                transform="rotate(90 12 12)">{t.progress ?? 0}%</text>
            </svg>
          {:else if t.error}
            <svg class="w-3.5 h-3.5 shrink-0 text-red-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
            </svg>
          {:else}
            <svg class="w-3.5 h-3.5 shrink-0 text-green-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          {/if}
          <span class="truncate {t.error ? 'text-red-300' : 'text-zinc-200'}">
            {#if t.kind === 'upload'}
              {t.done ? 'Uploaded' : 'Uploading'}: {t.label}
            {:else}
              {t.label}
            {/if}
          </span>
          {#if t.error}
            <span class="text-red-400 shrink-0 truncate max-w-24">{t.error}</span>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
  </div><!-- end right column -->
</div>

<!-- ── Snippets ── -->

{#snippet panelItem(panel: PanelSnapshot, isPinned: boolean)}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="group mx-1.5 mb-0.5">
    <div class="flex items-center rounded transition
      {activePanelId === panel.id
        ? 'bg-blue-600/25 text-zinc-100 ring-1 ring-inset ring-blue-600/40'
        : 'text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200'}">
      <div class="pl-2 py-1.5 shrink-0 opacity-60">
        <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
        </svg>
      </div>
      {#if renamingPanelId === panel.id}
        <!-- svelte-ignore a11y_autofocus -->
        <input
          type="text"
          autofocus
          bind:value={renamePanelValue}
          class="flex-1 min-w-0 mx-1.5 my-1 text-xs bg-zinc-800 border border-blue-500 rounded px-1.5 py-0.5 outline-none text-zinc-100"
          onclick={(e) => e.stopPropagation()}
          onblur={commitRenamePanel}
          onkeydown={(e) => {
            e.stopPropagation();
            if (e.key === 'Enter') commitRenamePanel();
            if (e.key === 'Escape') { renamingPanelId = null; }
          }}
        />
      {:else}
        <button
          class="flex-1 min-w-0 text-left text-xs py-1.5 pl-1.5 pr-1 truncate"
          onclick={() => switchToPanel(panel.id)}
          ondblclick={() => { if (activePanelId === panel.id) startRenamePanel(panel.id, panel.label); }}
          title={panel.path}
        >{panel.label}</button>
      {/if}
      {#if renamingPanelId !== panel.id}
        <div class="flex items-center opacity-0 group-hover:opacity-100 pr-1 gap-0.5 shrink-0">
          <button
            onclick={(e) => { e.stopPropagation(); isPinned ? unpinPanel(panel.id) : pinPanel(panel.id); }}
            title={isPinned ? 'Unpin' : 'Pin'}
            class="p-0.5 rounded hover:bg-zinc-700 transition {isPinned ? 'text-yellow-400' : 'text-zinc-500 hover:text-zinc-300'}"
          >
            <svg class="w-3 h-3" viewBox="0 0 24 24" fill={isPinned ? 'currentColor' : 'none'} stroke="currentColor" stroke-width="2">
              <line x1="12" y1="17" x2="12" y2="22"/>
              <path d="M5 17h14v-1.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V6h1a2 2 0 0 0 0-4H8a2 2 0 0 0 0 4h1v4.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24Z"/>
            </svg>
          </button>
          <button
            onclick={(e) => { e.stopPropagation(); panels.length > 1 && closePanel(panel.id); }}
            title="Close panel"
            class="p-0.5 rounded hover:bg-red-500/20 hover:text-red-400 text-zinc-500 transition
                   {panels.length <= 1 ? 'opacity-30 cursor-not-allowed' : ''}"
          >
            <svg class="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>
      {/if}
    </div>
  </div>
{/snippet}

{#snippet fileIcon(kind: IconKind)}
  {#if kind === 'folder'}
    <svg viewBox="0 0 24 24" fill="none" class="w-11 h-11 text-yellow-400" stroke="currentColor" stroke-width="1.5">
      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" fill="currentColor" fill-opacity=".18"/>
      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
    </svg>
  {:else if kind === 'image'}
    <svg viewBox="0 0 24 24" fill="none" class="w-11 h-11 text-green-400" stroke="currentColor" stroke-width="1.5">
      <rect x="3" y="3" width="18" height="18" rx="2" fill="currentColor" fill-opacity=".12"/>
      <rect x="3" y="3" width="18" height="18" rx="2"/>
      <circle cx="8.5" cy="8.5" r="1.5" fill="currentColor"/>
      <polyline points="21 15 16 10 5 21"/>
    </svg>
  {:else if kind === 'video'}
    <svg viewBox="0 0 24 24" fill="none" class="w-11 h-11 text-purple-400" stroke="currentColor" stroke-width="1.5">
      <rect x="2" y="4" width="20" height="16" rx="2" fill="currentColor" fill-opacity=".12"/>
      <rect x="2" y="4" width="20" height="16" rx="2"/>
      <polygon points="10 9 15 12 10 15" fill="currentColor" stroke="none"/>
    </svg>
  {:else if kind === 'audio'}
    <svg viewBox="0 0 24 24" fill="none" class="w-11 h-11 text-pink-400" stroke="currentColor" stroke-width="1.5">
      <path d="M9 18V5l12-2v13" fill="currentColor" fill-opacity=".12"/>
      <path d="M9 18V5l12-2v13"/>
      <circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>
    </svg>
  {:else if kind === 'archive'}
    <svg viewBox="0 0 24 24" fill="none" class="w-11 h-11 text-orange-400" stroke="currentColor" stroke-width="1.5">
      <polyline points="21 8 21 21 3 21 3 8" fill="currentColor" fill-opacity=".12"/>
      <rect x="1" y="3" width="22" height="5" rx="1"/>
      <line x1="10" y1="12" x2="14" y2="12"/>
    </svg>
  {:else if kind === 'code'}
    <svg viewBox="0 0 24 24" fill="none" class="w-11 h-11 text-blue-400" stroke="currentColor" stroke-width="1.5">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" fill="currentColor" fill-opacity=".12"/>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
      <polyline points="14 2 14 8 20 8"/>
      <polyline points="9 13 7 15 9 17"/><polyline points="15 13 17 15 15 17"/>
    </svg>
  {:else if kind === 'pdf'}
    <svg viewBox="0 0 24 24" fill="none" class="w-11 h-11 text-red-400" stroke="currentColor" stroke-width="1.5">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" fill="currentColor" fill-opacity=".12"/>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
      <polyline points="14 2 14 8 20 8"/>
      <text x="6" y="19" font-size="4.5" fill="currentColor" stroke="none" font-weight="bold">PDF</text>
    </svg>
  {:else}
    <svg viewBox="0 0 24 24" fill="none" class="w-11 h-11 text-zinc-400" stroke="currentColor" stroke-width="1.5">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" fill="currentColor" fill-opacity=".12"/>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
      <polyline points="14 2 14 8 20 8"/>
    </svg>
  {/if}
{/snippet}

{#snippet iconUp()}
  <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="15 18 9 12 15 6"/></svg>
{/snippet}
{#snippet iconHome()}
  <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/></svg>
{/snippet}
{#snippet iconRefresh()}
  <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>
{/snippet}
{#snippet iconFolderPlus()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><line x1="12" y1="11" x2="12" y2="17"/><line x1="9" y1="14" x2="15" y2="14"/></svg>
{/snippet}
{#snippet iconFilePlus()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="12" y1="18" x2="12" y2="12"/><line x1="9" y1="15" x2="15" y2="15"/></svg>
{/snippet}
{#snippet iconUpload()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="16 16 12 12 8 16"/><line x1="12" y1="12" x2="12" y2="21"/><path d="M20.39 18.39A5 5 0 0 0 18 9h-1.26A8 8 0 1 0 3 16.3"/></svg>
{/snippet}
{#snippet iconFolderUpload()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><polyline points="16 13 12 9 8 13"/><line x1="12" y1="9" x2="12" y2="17"/></svg>
{/snippet}
{#snippet iconDownload()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="8 17 12 21 16 17"/><line x1="12" y1="12" x2="12" y2="21"/><path d="M20.88 18.09A5 5 0 0 0 18 9h-1.26A8 8 0 1 0 3 16.29"/></svg>
{/snippet}
{#snippet iconCopy()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
{/snippet}
{#snippet iconCut()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="6" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><line x1="20" y1="4" x2="8.12" y2="15.88"/><line x1="14.47" y1="14.48" x2="20" y2="20"/><line x1="8.12" y1="8.12" x2="12" y2="12"/></svg>
{/snippet}
{#snippet iconPaste()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><rect x="8" y="2" width="8" height="4" rx="1"/></svg>
{/snippet}
{#snippet iconTrash()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
{/snippet}
{#snippet iconEye()}
  <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
{/snippet}

