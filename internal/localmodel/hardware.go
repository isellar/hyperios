// Package localmodel detects available local compute resources (GPU VRAM,
// system RAM) and picks an appropriately-sized local LLM to run via Ollama,
// so HyperiOS can avoid paid API calls where possible. See catalog.go for the
// curated model list and hybrid.go for the local-first/remote-fallback
// Completer that ties it all together.
package localmodel

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// GPU describes a single detected GPU.
type GPU struct {
	Name        string
	VRAMTotalMB int
	VRAMFreeMB  int
}

// Hardware describes the compute resources detected on this machine.
type Hardware struct {
	// GPUs lists every detected GPU (empty if none, or nvidia-smi unavailable).
	// Ollama can shard a single model across multiple GPUs via its own
	// multi-GPU tensor-split logic, so for model-fit purposes we generally
	// care about VRAMTotalMB (the sum) rather than any single card.
	GPUs []GPU
	// GPUName is the first detected GPU's name, or "" if none. Kept for
	// backwards-compat / simple display; see GPUs for the full list.
	GPUName string
	// VRAMTotalMB is the combined VRAM across all detected GPUs, in MB, or 0
	// if none were detected.
	VRAMTotalMB int
	// VRAMFreeMB is the combined free VRAM across all detected GPUs, in MB.
	VRAMFreeMB int
	// SystemRAMTotalMB is total system RAM in MB.
	SystemRAMTotalMB int
	// SystemRAMAvailableMB is currently-available system RAM in MB.
	SystemRAMAvailableMB int
	// CPUCores is the number of logical CPU cores.
	CPUCores int
}

// HasGPU reports whether at least one usable GPU (with detected VRAM) was found.
func (h Hardware) HasGPU() bool {
	return h.VRAMTotalMB > 0
}

// DetectHardware inspects the local machine for GPU and system memory
// resources. It never returns an error — detection failures simply leave the
// corresponding fields at their zero value, so callers always get a usable
// (if conservative) Hardware value to make decisions from.
func DetectHardware() Hardware {
	h := Hardware{CPUCores: cpuCount()}

	if gpus, ok := detectNvidiaGPUs(); ok {
		h.GPUs = gpus
		h.GPUName = gpus[0].Name
		for _, g := range gpus {
			h.VRAMTotalMB += g.VRAMTotalMB
			h.VRAMFreeMB += g.VRAMFreeMB
		}
	}

	total, avail := detectSystemRAM()
	h.SystemRAMTotalMB = total
	h.SystemRAMAvailableMB = avail

	return h
}

// detectNvidiaGPUs shells out to nvidia-smi, if present, to read GPU name and
// VRAM for every installed card. Returns ok=false if nvidia-smi is not
// installed, fails, or reports no GPUs (e.g. no NVIDIA GPU present, or
// running in a container without GPU passthrough).
//
// Multiple GPUs are summed for total-VRAM model-fit decisions (see
// Hardware.VRAMTotalMB) since Ollama can tensor-split a single model across
// them; this does not attempt to model the extra inter-GPU transfer overhead
// that multi-GPU splitting incurs versus a single card with equivalent VRAM.
func detectNvidiaGPUs() ([]GPU, bool) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, false
	}

	out, err := exec.Command(path,
		"--query-gpu=name,memory.total,memory.free",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return nil, false
	}

	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) != 3 {
			continue
		}
		total, err1 := strconv.Atoi(strings.TrimSpace(fields[1]))
		free, err2 := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err1 != nil || err2 != nil {
			continue
		}
		gpus = append(gpus, GPU{
			Name:        strings.TrimSpace(fields[0]),
			VRAMTotalMB: total,
			VRAMFreeMB:  free,
		})
	}

	if len(gpus) == 0 {
		return nil, false
	}
	return gpus, true
}

// detectSystemRAM reads total and available memory from /proc/meminfo (Linux
// only, matching the project's target platform). Returns zero values if
// unreadable.
func detectSystemRAM() (totalMB, availableMB int) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			totalMB = parseMeminfoKB(line) / 1024
		case strings.HasPrefix(line, "MemAvailable:"):
			availableMB = parseMeminfoKB(line) / 1024
		}
	}
	return totalMB, availableMB
}

// parseMeminfoKB parses a "Key:    12345 kB" line from /proc/meminfo into KB.
func parseMeminfoKB(line string) int {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return n
}

// cpuCount returns the number of logical CPUs available to this process.
func cpuCount() int {
	if n := runtime.NumCPU(); n > 0 {
		return n
	}
	return 1
}
