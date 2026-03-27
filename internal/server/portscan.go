package server

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PortInfo describes a single TCP port that is accepting connections.
type PortInfo struct {
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Process string `json:"process"` // comm name (e.g. "bun")
	Cmdline string `json:"cmdline"` // full command line, truncated to 120 chars
}

// scanListeningPorts reads /proc/net/tcp and /proc/net/tcp6 for LISTEN sockets
// (state 0A), resolves the owning PID via /proc/*/fd symlinks, and returns
// a port-sorted slice of PortInfo.
func scanListeningPorts() []PortInfo {
	// inode → port
	inodePorts := map[uint64]int{}

	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // skip header line
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 {
				continue
			}
			if fields[3] != "0A" { // TCP_LISTEN
				continue
			}
			// local_address: hex_ip:hex_port
			parts := strings.SplitN(fields[1], ":", 2)
			if len(parts) != 2 {
				continue
			}
			port, err := strconv.ParseInt(parts[1], 16, 32)
			if err != nil {
				continue
			}
			inode, err := strconv.ParseUint(fields[9], 10, 64)
			if err != nil {
				continue
			}
			if _, exists := inodePorts[inode]; !exists {
				inodePorts[inode] = int(port)
			}
		}
		f.Close()
	}

	if len(inodePorts) == 0 {
		return nil
	}

	// Walk /proc/*/fd/* to map socket inodes → PIDs.
	inodePID := map[uint64]int{}
	fds, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	for _, fd := range fds {
		target, err := os.Readlink(fd)
		if err != nil || !strings.HasPrefix(target, "socket:[") {
			continue
		}
		inodeStr := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
		inode, err := strconv.ParseUint(inodeStr, 10, 64)
		if err != nil || inodePorts[inode] == 0 {
			continue
		}
		parts := strings.Split(fd, "/") // /proc/{pid}/fd/{n}
		if len(parts) < 3 {
			continue
		}
		pid, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}
		inodePID[inode] = pid
	}

	// Deduplicate: one entry per port.
	portPID := map[int]int{}
	for inode, port := range inodePorts {
		if pid, ok := inodePID[inode]; ok {
			portPID[port] = pid
		} else if _, exists := portPID[port]; !exists {
			portPID[port] = 0
		}
	}

	result := make([]PortInfo, 0, len(portPID))
	for port, pid := range portPID {
		info := PortInfo{Port: port, PID: pid}
		if pid > 0 {
			info.Process = procComm(pid)
			info.Cmdline = procCmdline(pid)
		}
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Port < result[j].Port })
	return result
}

func procComm(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func procCmdline(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	// NUL-separated args → space-joined
	s := strings.Join(strings.Split(strings.TrimRight(string(data), "\x00"), "\x00"), " ")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
