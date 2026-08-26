package resources

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Memory struct {
	UsedBytes      uint64
	AvailableBytes uint64
	TotalBytes     uint64
	Valid          bool
}

type GPUHeap struct {
	UsedBytes  uint64
	TotalBytes uint64
	Valid      bool
}

type GPU struct {
	Card  string
	VRAM  GPUHeap
	GTT   GPUHeap
	Valid bool
}

type Snapshot struct {
	RAM Memory
	GPU GPU
}

type LinuxSampler struct {
	meminfoPath string
	drmRoot     string
}

func NewLinuxSampler(meminfoPath, drmRoot string) LinuxSampler {
	return LinuxSampler{meminfoPath: meminfoPath, drmRoot: drmRoot}
}

func (sampler LinuxSampler) Sample() Snapshot {
	return Snapshot{RAM: readMemory(sampler.meminfoPath), GPU: readGPU(sampler.drmRoot)}
}

func readGPU(root string) GPU {
	entries, err := os.ReadDir(root)
	if err != nil {
		return GPU{}
	}
	type candidate struct {
		card   string
		device string
	}
	candidates := make([]candidate, 0, 1)
	for _, entry := range entries {
		if !isDRMCard(entry.Name()) {
			continue
		}
		device := filepath.Join(root, entry.Name(), "device")
		if !isAMDGPU(device) {
			continue
		}
		candidates = append(candidates, candidate{card: entry.Name(), device: device})
	}
	if len(candidates) != 1 {
		return GPU{}
	}
	selected := candidates[0]
	vram := readGPUHeap(selected.device, "vram")
	gtt := readGPUHeap(selected.device, "gtt")
	if !vram.Valid && !gtt.Valid {
		return GPU{}
	}
	return GPU{Card: selected.card, VRAM: vram, GTT: gtt, Valid: true}
}

func isDRMCard(name string) bool {
	if !strings.HasPrefix(name, "card") || len(name) == len("card") {
		return false
	}
	for _, character := range name[len("card"):] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func isAMDGPU(device string) bool {
	vendor, err := os.ReadFile(filepath.Join(device, "vendor"))
	if err != nil || strings.ToLower(strings.TrimSpace(string(vendor))) != "0x1002" {
		return false
	}
	driver, err := filepath.EvalSymlinks(filepath.Join(device, "driver"))
	return err == nil && filepath.Base(driver) == "amdgpu"
}

func readGPUHeap(device, heap string) GPUHeap {
	used, usedOK := readUintFile(filepath.Join(device, "mem_info_"+heap+"_used"))
	total, totalOK := readUintFile(filepath.Join(device, "mem_info_"+heap+"_total"))
	if !usedOK || !totalOK || used > total {
		return GPUHeap{}
	}
	return GPUHeap{UsedBytes: used, TotalBytes: total, Valid: true}
}

func readUintFile(path string) (uint64, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
	return value, err == nil
}

func readMemory(path string) Memory {
	content, err := os.ReadFile(path)
	if err != nil {
		return Memory{}
	}
	values := make(map[string]uint64)
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != "kB" {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if key != "MemTotal" && key != "MemAvailable" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || value > math.MaxUint64/1024 {
			continue
		}
		values[key] = value * 1024
	}
	total, hasTotal := values["MemTotal"]
	available, hasAvailable := values["MemAvailable"]
	if !hasTotal || !hasAvailable || available > total {
		return Memory{}
	}
	return Memory{UsedBytes: total - available, AvailableBytes: available, TotalBytes: total, Valid: true}
}
