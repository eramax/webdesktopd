// Package stats reads Linux /proc metrics and broadcasts them as 0x03 frames.
package stats

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"webdesktopd/internal/hub"
)

const tickInterval = time.Second

// Snapshot is a full stats payload (sent once on connect).
type Snapshot struct {
	CPU       float64   `json:"cpu"`
	RAMUsed   uint64    `json:"ramUsed"`
	RAMTotal  uint64    `json:"ramTotal"`
	DiskUsed  uint64    `json:"diskUsed"`
	DiskTotal uint64    `json:"diskTotal"`
	NetRxRate uint64    `json:"netRxRate"`
	NetTxRate uint64    `json:"netTxRate"`
	Uptime    float64   `json:"uptime"`
	LoadAvg   []float64 `json:"loadAvg"`
	Kernel    string    `json:"kernel"`
	Hostname  string    `json:"hostname"`
}

// StatsDelta is the FrameStats (0x03) payload sent every tick.
// Only fields that changed since the previous frame are non-nil / non-empty.
// The first frame a client receives is always a full Snapshot (all fields set);
// subsequent frames only carry what actually changed.
type StatsDelta struct {
	CPU       *float64  `json:"cpu,omitempty"`
	RAMUsed   *uint64   `json:"ramUsed,omitempty"`
	RAMTotal  *uint64   `json:"ramTotal,omitempty"`
	DiskUsed  *uint64   `json:"diskUsed,omitempty"`
	DiskTotal *uint64   `json:"diskTotal,omitempty"`
	NetRxRate *uint64   `json:"netRxRate,omitempty"`
	NetTxRate *uint64   `json:"netTxRate,omitempty"`
	Uptime    *float64  `json:"uptime,omitempty"`
	LoadAvg   []float64 `json:"loadAvg,omitempty"`
	Kernel    *string   `json:"kernel,omitempty"`
	Hostname  *string   `json:"hostname,omitempty"`
}

// Sender is any value that can receive a hub frame (e.g. *hub.Hub).
type Sender interface {
	Send(f hub.Frame) error
}

// Collector broadcasts StatsDelta frames to all registered senders every second.
// It is ref-counted: the background goroutine runs only while at least one
// sender is registered, and stops automatically when the last one leaves.
//
// When a new sender joins via Add, it immediately receives the last full
// Snapshot so it has all fields (including static ones like kernel/hostname).
// Subsequent ticks broadcast only the fields that changed.
type Collector struct {
	mu      sync.Mutex
	senders map[uint64]Sender
	cancel  context.CancelFunc // non-nil while running
	refs    int
	last    Snapshot // last collected snapshot, protected by mu

	// Cached at startup (don't change at runtime).
	kernel   string
	hostname string

	// Monotonic counter used to generate sender IDs.
	idGen atomic.Uint64
}

// New creates a Collector and pre-reads static system info.
func New() *Collector {
	c := &Collector{
		senders: make(map[uint64]Sender),
	}
	c.kernel = readKernel()
	if h, err := os.Hostname(); err == nil {
		c.hostname = h
	}
	return c
}

// Add registers a sender and starts the collector loop if this is the first.
// If a snapshot has already been collected, the new sender immediately receives
// a full Snapshot frame so it has all static fields (kernel, hostname, totals).
// Returns an ID that must be passed to Remove when done.
func (c *Collector) Add(s Sender) uint64 {
	id := c.idGen.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.senders[id] = s
	c.refs++
	if c.refs == 1 {
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel = cancel
		go c.run(ctx)
		slog.Info("stats: collector started")
	} else if c.last.RAMTotal > 0 {
		// Send the last full snapshot to the new sender immediately so it
		// has kernel/hostname/totals before the next tick fires.
		if data, err := json.Marshal(snapshotToDelta(c.last, Snapshot{})); err == nil {
			_ = s.Send(hub.Frame{Type: hub.FrameStats, ChanID: 0, Payload: data})
		}
	}
	return id
}

// Remove unregisters a sender and stops the loop when the last one leaves.
func (c *Collector) Remove(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.senders[id]; !ok {
		return
	}
	delete(c.senders, id)
	c.refs--
	if c.refs == 0 && c.cancel != nil {
		c.cancel()
		c.cancel = nil
		slog.Info("stats: collector stopped")
	}
}

// run is the background goroutine. It takes two samples one second apart to
// compute CPU and network rates, then broadcasts a StatsDelta to all senders.
func (c *Collector) run(ctx context.Context) {
	var prevCPU cpuSample
	var prevNet netSample

	// Prime the samples before the first tick.
	prevCPU, _ = readCPU()
	prevNet, _ = readNet()

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		snap, err := c.collect(&prevCPU, &prevNet)
		if err != nil {
			slog.Debug("stats: collect error", "err", err)
			continue
		}

		c.mu.Lock()
		delta := snapshotToDelta(snap, c.last)
		c.last = snap
		c.mu.Unlock()

		data, err := json.Marshal(delta)
		if err != nil {
			continue
		}

		frame := hub.Frame{Type: hub.FrameStats, ChanID: 0, Payload: data}

		c.mu.Lock()
		for id, s := range c.senders {
			if err := s.Send(frame); err != nil {
				slog.Debug("stats: send error, removing sender", "id", id, "err", err)
				delete(c.senders, id)
				c.refs--
			}
		}
		c.mu.Unlock()
	}
}

// snapshotToDelta returns a StatsDelta containing only the fields of cur that
// differ from prev. Pass a zero prev to get a delta with all fields set (full
// snapshot equivalent).
func snapshotToDelta(cur, prev Snapshot) StatsDelta {
	d := StatsDelta{}

	// Always send the rapidly-changing fields.
	d.CPU = &cur.CPU
	d.NetRxRate = &cur.NetRxRate
	d.NetTxRate = &cur.NetTxRate
	d.Uptime = &cur.Uptime

	// Send slowly-changing fields only when they differ.
	if cur.RAMUsed != prev.RAMUsed {
		d.RAMUsed = &cur.RAMUsed
	}
	if cur.DiskUsed != prev.DiskUsed {
		d.DiskUsed = &cur.DiskUsed
	}
	// loadAvg: compare element-wise; send if any value changed.
	if !loadAvgEqual(cur.LoadAvg, prev.LoadAvg) {
		d.LoadAvg = cur.LoadAvg
	}

	// Send static fields only when they differ from prev (effectively once).
	if cur.RAMTotal != prev.RAMTotal {
		d.RAMTotal = &cur.RAMTotal
	}
	if cur.DiskTotal != prev.DiskTotal {
		d.DiskTotal = &cur.DiskTotal
	}
	if cur.Kernel != prev.Kernel {
		d.Kernel = &cur.Kernel
	}
	if cur.Hostname != prev.Hostname {
		d.Hostname = &cur.Hostname
	}

	return d
}

func loadAvgEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *Collector) collect(prevCPU *cpuSample, prevNet *netSample) (Snapshot, error) {
	curCPU, err := readCPU()
	if err != nil {
		return Snapshot{}, fmt.Errorf("cpu: %w", err)
	}
	cpuPct := cpuPercent(*prevCPU, curCPU)
	*prevCPU = curCPU

	curNet, err := readNet()
	if err != nil {
		return Snapshot{}, fmt.Errorf("net: %w", err)
	}
	rxRate, txRate := netRates(*prevNet, curNet)
	*prevNet = curNet

	ramUsed, ramTotal, err := readMem()
	if err != nil {
		return Snapshot{}, fmt.Errorf("mem: %w", err)
	}

	diskUsed, diskTotal, err := readDisk()
	if err != nil {
		return Snapshot{}, fmt.Errorf("disk: %w", err)
	}

	uptime, err := readUptime()
	if err != nil {
		return Snapshot{}, fmt.Errorf("uptime: %w", err)
	}

	loadAvg, err := readLoadAvg()
	if err != nil {
		return Snapshot{}, fmt.Errorf("loadavg: %w", err)
	}

	return Snapshot{
		CPU:       cpuPct,
		RAMUsed:   ramUsed,
		RAMTotal:  ramTotal,
		DiskUsed:  diskUsed,
		DiskTotal: diskTotal,
		NetRxRate: rxRate,
		NetTxRate: txRate,
		Uptime:    uptime,
		LoadAvg:   loadAvg,
		Kernel:    c.kernel,
		Hostname:  c.hostname,
	}, nil
}

// ── CPU ──────────────────────────────────────────────────────────────────────

type cpuSample struct {
	total uint64
	idle  uint64
}

func readCPU() (cpuSample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		// fields[0]="cpu" fields[1..10] = user nice system idle iowait irq softirq steal guest guest_nice
		if len(fields) < 5 {
			break
		}
		var vals [10]uint64
		for i := 1; i < len(fields) && i-1 < 10; i++ {
			vals[i-1], _ = strconv.ParseUint(fields[i], 10, 64)
		}
		idle := vals[3] + vals[4] // idle + iowait
		total := vals[0] + vals[1] + vals[2] + vals[3] + vals[4] + vals[5] + vals[6] + vals[7]
		return cpuSample{total: total, idle: idle}, nil
	}
	return cpuSample{}, fmt.Errorf("no cpu line in /proc/stat")
}

func cpuPercent(prev, cur cpuSample) float64 {
	totalDelta := cur.total - prev.total
	idleDelta := cur.idle - prev.idle
	if totalDelta == 0 {
		return 0
	}
	used := totalDelta - idleDelta
	return float64(used) / float64(totalDelta) * 100.0
}

// ── Memory ───────────────────────────────────────────────────────────────────

func readMem() (used, total uint64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var memTotal, memAvail uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			memTotal = val * 1024
		case "MemAvailable:":
			memAvail = val * 1024
		}
	}
	return memTotal - memAvail, memTotal, nil
}

// ── Disk ─────────────────────────────────────────────────────────────────────

func readDisk() (used, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return 0, 0, err
	}
	blockSize := uint64(st.Bsize)
	total = st.Blocks * blockSize
	avail := st.Bavail * blockSize
	used = total - avail
	return used, total, nil
}

// ── Network ──────────────────────────────────────────────────────────────────

type netSample struct {
	rx uint64
	tx uint64
}

func readNet() (netSample, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return netSample{}, err
	}
	defer f.Close()

	var rx, tx uint64
	scanner := bufio.NewScanner(f)
	// Skip the two header lines.
	scanner.Scan()
	scanner.Scan()
	for scanner.Scan() {
		line := scanner.Text()
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colon])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		t, _ := strconv.ParseUint(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return netSample{rx: rx, tx: tx}, nil
}

func netRates(prev, cur netSample) (rxRate, txRate uint64) {
	if cur.rx >= prev.rx {
		rxRate = cur.rx - prev.rx
	}
	if cur.tx >= prev.tx {
		txRate = cur.tx - prev.tx
	}
	return
}

// ── Uptime ───────────────────────────────────────────────────────────────────

func readUptime() (float64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// ── Load average ─────────────────────────────────────────────────────────────

func readLoadAvg() ([]float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil, fmt.Errorf("short /proc/loadavg")
	}
	result := make([]float64, 3)
	for i := range 3 {
		result[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return result, nil
}

// ── Kernel version ───────────────────────────────────────────────────────────

func readKernel() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return ""
	}
	// Utsname.Release is [65]int8 on Linux amd64.
	b := make([]byte, 0, 64)
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}
