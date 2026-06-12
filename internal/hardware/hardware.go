// Package hardware detects the host's compute capabilities (CPU/GPU/NPU,
// memory, disk) and classifies the machine into a profile so the rest of the
// tool can pick silicon-optimal defaults. Linux-only; degrades gracefully when
// a probe tool is missing.
package hardware

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// GPU describes a single graphics/compute device.
type GPU struct {
	Vendor string // nvidia | amd | intel | unknown
	Name   string
	VRAMGB int    // 0 if unknown
	Driver string // driver/runtime version if known
	Accel  string // accelerator keyword: cuda | rocm | intel-gpu | ""
}

// Info is a snapshot of the host.
type Info struct {
	CPUModel string
	Threads  int
	ISA      string // best vector ISA: avx512 | avx2 | avx | neon | generic
	MemGB    float64
	AvailGB  float64
	DiskGB   float64 // free on /
	GPUs     []GPU
	NPU      string // NPU device name, "" if none
	Battery  bool
	Headless bool
	Profile  string // edge | laptop | workstation | server
}

// Detect probes the host and returns a populated Info.
func Detect() *Info {
	i := &Info{}
	i.detectCPU()
	i.detectMem()
	i.detectDisk()
	i.detectGPU()
	i.detectNPU()
	i.Battery = hasBattery()
	i.Headless = os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
	i.Profile = i.classify()
	return i
}

func (i *Info) detectCPU() {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return
	}
	defer f.Close()
	var flags string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "model name":
			if i.CPUModel == "" {
				i.CPUModel = v
			}
			i.Threads++
		case "processor":
			// counted via model name lines instead
		case "flags", "Features":
			flags = v
		}
	}
	switch {
	case strings.Contains(flags, "avx512f"):
		i.ISA = "avx512"
	case strings.Contains(flags, "avx2"):
		i.ISA = "avx2"
	case strings.Contains(flags, "avx"):
		i.ISA = "avx"
	case strings.Contains(flags, "asimd") || strings.Contains(flags, "neon"):
		i.ISA = "neon"
	default:
		i.ISA = "generic"
	}
	if i.Threads == 0 {
		i.Threads = 1
	}
}

func (i *Info) detectMem() {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		kb := parseFloat(fields[1])
		switch fields[0] {
		case "MemTotal:":
			i.MemGB = kb / 1024 / 1024
		case "MemAvailable:":
			i.AvailGB = kb / 1024 / 1024
		}
	}
}

func (i *Info) detectDisk() {
	// Inside strict confinement "/" is the base snap, not the host disk; measure a
	// host-visible path (the real home) so free space reflects the actual disk.
	path := "/"
	if h := os.Getenv("SNAP_REAL_HOME"); h != "" {
		path = h
	} else if h, err := os.UserHomeDir(); err == nil && h != "" {
		path = h
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err == nil {
		i.DiskGB = float64(st.Bavail) * float64(st.Bsize) / 1e9
	}
}

func (i *Info) detectGPU() {
	// NVIDIA via nvidia-smi (authoritative for VRAM + driver).
	if out, err := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.total,driver_version", "--format=csv,noheader,nounits").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Split(line, ",")
			if len(parts) >= 3 {
				i.GPUs = append(i.GPUs, GPU{
					Vendor: "nvidia",
					Name:   strings.TrimSpace(parts[0]),
					VRAMGB: int(parseFloat(strings.TrimSpace(parts[1])) / 1024),
					Driver: strings.TrimSpace(parts[2]),
					Accel:  "cuda",
				})
			}
		}
	}
	// AMD/Intel and any others via lspci (no VRAM, but vendor + presence).
	if out, err := exec.Command("lspci").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			l := strings.ToLower(line)
			if !strings.Contains(l, "vga") && !strings.Contains(l, "3d controller") && !strings.Contains(l, "display controller") {
				continue
			}
			name := line
			if idx := strings.Index(line, ": "); idx >= 0 {
				name = line[idx+2:]
			}
			switch {
			case strings.Contains(l, "nvidia"):
				// already covered by nvidia-smi; skip to avoid duplicates
			case strings.Contains(l, "amd") || strings.Contains(l, "advanced micro devices") || strings.Contains(l, "radeon"):
				i.GPUs = append(i.GPUs, GPU{Vendor: "amd", Name: name, Accel: "rocm"})
			case strings.Contains(l, "intel"):
				i.GPUs = append(i.GPUs, GPU{Vendor: "intel", Name: name, Accel: "intel-gpu"})
			}
		}
	}
}

func (i *Info) detectNPU() {
	// Intel/AMD NPUs expose /dev/accelN via the accel subsystem.
	if matches, _ := filepath.Glob("/dev/accel*"); len(matches) > 0 {
		i.NPU = "accelerator (npu)"
		// Try to name it from the sysfs accel class.
		if devs, _ := filepath.Glob("/sys/class/accel/accel*/device/uevent"); len(devs) > 0 {
			if b, err := os.ReadFile(devs[0]); err == nil {
				for _, ln := range strings.Split(string(b), "\n") {
					if strings.HasPrefix(ln, "DRIVER=") {
						i.NPU = strings.TrimPrefix(ln, "DRIVER=") + " npu"
					}
				}
			}
		}
	}
}

func hasBattery() bool {
	entries, _ := filepath.Glob("/sys/class/power_supply/BAT*")
	return len(entries) > 0
}

// classify maps the snapshot to a machine profile.
func (i *Info) classify() string {
	hasDiscreteGPU := false
	gpuCount := 0
	for _, g := range i.GPUs {
		gpuCount++
		if g.Vendor == "nvidia" || g.Vendor == "amd" {
			hasDiscreteGPU = true
		}
	}
	switch {
	case i.MemGB > 0 && i.MemGB <= 4, i.ISA == "neon" && !hasDiscreteGPU && i.MemGB <= 8:
		return "edge"
	case i.Battery:
		return "laptop"
	case i.Headless && (i.Threads >= 32 || gpuCount >= 2):
		return "server"
	case hasDiscreteGPU:
		return "workstation"
	case i.Headless:
		return "server"
	default:
		return "workstation"
	}
}

// Accelerators returns the accelerator keywords usable on this host, best first,
// always ending with "cpu".
func (i *Info) Accelerators() []string {
	var accs []string
	seen := map[string]bool{}
	for _, g := range i.GPUs {
		if g.Accel != "" && !seen[g.Accel] {
			accs = append(accs, g.Accel)
			seen[g.Accel] = true
		}
	}
	if i.NPU != "" && !seen["npu"] {
		accs = append(accs, "npu")
	}
	accs = append(accs, "cpu")
	return accs
}

// PrimaryGPU returns the most capable GPU, or nil.
func (i *Info) PrimaryGPU() *GPU {
	var best *GPU
	for idx := range i.GPUs {
		g := &i.GPUs[idx]
		if best == nil {
			best = g
			continue
		}
		// Prefer discrete (has VRAM/cuda/rocm) over integrated.
		if g.VRAMGB > best.VRAMGB {
			best = g
		}
	}
	return best
}

func parseFloat(s string) float64 {
	var f float64
	var neg bool
	var dec float64 = 0
	var inDec bool
	for _, r := range s {
		switch {
		case r == '-' && f == 0 && !inDec:
			neg = true
		case r == '.':
			inDec = true
			dec = 0.1
		case r >= '0' && r <= '9':
			d := float64(r - '0')
			if inDec {
				f += d * dec
				dec /= 10
			} else {
				f = f*10 + d
			}
		default:
			// stop at first non-numeric (e.g. units)
			if f != 0 || inDec {
				goto done
			}
		}
	}
done:
	if neg {
		return -f
	}
	return f
}
