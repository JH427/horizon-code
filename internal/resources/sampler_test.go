package resources

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGPUFixture(t *testing.T, drm, card, vendor, driver string, values map[string]string) {
	t.Helper()
	device := filepath.Join(drm, card, "device")
	if err := os.MkdirAll(device, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(device, "vendor"), []byte(vendor+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	driverTarget := filepath.Join(drm, "drivers", driver)
	if err := os.MkdirAll(driverTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driverTarget, filepath.Join(device, "driver")); err != nil {
		t.Fatal(err)
	}
	for name, value := range values {
		if err := os.WriteFile(filepath.Join(device, name), []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSamplerSelectsOnlyExactAMDGPUCardAndPreservesZero(t *testing.T) {
	root := t.TempDir()
	drm := filepath.Join(root, "drm")
	desired := map[string]string{
		"mem_info_vram_used": "0", "mem_info_vram_total": "1073741824",
		"mem_info_gtt_used": "536870912", "mem_info_gtt_total": "133143986176",
	}
	larger := map[string]string{
		"mem_info_vram_used": "1", "mem_info_vram_total": "10737418240",
		"mem_info_gtt_used": "2", "mem_info_gtt_total": "268435456000",
	}
	writeGPUFixture(t, drm, "card1", "0x1002", "amdgpu", desired)
	writeGPUFixture(t, drm, "card0", "0x8086", "i915", larger)
	writeGPUFixture(t, drm, "card1-DP-1", "0x1002", "amdgpu", larger)

	snapshot := NewLinuxSampler(filepath.Join(root, "missing-meminfo"), drm).Sample()
	if !snapshot.GPU.Valid || snapshot.GPU.Card != "card1" {
		t.Fatalf("GPU sample = %#v", snapshot.GPU)
	}
	if !snapshot.GPU.VRAM.Valid || snapshot.GPU.VRAM.UsedBytes != 0 || snapshot.GPU.VRAM.TotalBytes != 1073741824 {
		t.Fatalf("VRAM sample = %#v", snapshot.GPU.VRAM)
	}
	if !snapshot.GPU.GTT.Valid || snapshot.GPU.GTT.UsedBytes != 536870912 || snapshot.GPU.GTT.TotalBytes != 133143986176 {
		t.Fatalf("GTT sample = %#v", snapshot.GPU.GTT)
	}
}

func TestSamplerRejectsAmbiguousAMDGPUs(t *testing.T) {
	root := t.TempDir()
	drm := filepath.Join(root, "drm")
	values := map[string]string{"mem_info_vram_used": "0", "mem_info_vram_total": "1024"}
	writeGPUFixture(t, drm, "card1", "0x1002", "amdgpu", values)
	writeGPUFixture(t, drm, "card2", "0x1002", "amdgpu", values)
	if gpu := NewLinuxSampler(filepath.Join(root, "missing"), drm).Sample().GPU; gpu.Valid {
		t.Fatalf("ambiguous GPU sample guessed a device: %#v", gpu)
	}
}

func TestSamplerRejectsAmbiguousAMDGPUsWhenOneHeapIsMalformed(t *testing.T) {
	root := t.TempDir()
	drm := filepath.Join(root, "drm")
	writeGPUFixture(t, drm, "card1", "0x1002", "amdgpu", map[string]string{
		"mem_info_vram_used": "0", "mem_info_vram_total": "1073741824",
	})
	writeGPUFixture(t, drm, "card2", "0x1002", "amdgpu", map[string]string{
		"mem_info_vram_used": "malformed", "mem_info_vram_total": "1073741824",
	})
	if gpu := NewLinuxSampler(filepath.Join(root, "missing"), drm).Sample().GPU; gpu.Valid {
		t.Fatalf("ambiguous exact AMD GPUs were guessed: %#v", gpu)
	}
}

func TestSamplerRejectsAmbiguousAMDGPUsWithLargeDigitOnlyCardSuffix(t *testing.T) {
	root := t.TempDir()
	drm := filepath.Join(root, "drm")
	writeGPUFixture(t, drm, "card0", "0x1002", "amdgpu", map[string]string{
		"mem_info_vram_used": "1", "mem_info_vram_total": "2",
	})
	writeGPUFixture(t, drm, "card4294967296", "0x1002", "amdgpu", map[string]string{
		"mem_info_vram_used": "malformed", "mem_info_vram_total": "2",
	})
	if gpu := NewLinuxSampler(filepath.Join(root, "missing"), drm).Sample().GPU; gpu.Valid {
		t.Fatalf("large exact card suffix bypassed ambiguity: %#v", gpu)
	}
}

func TestSamplerPreservesReadableZeroTotalGPUHeap(t *testing.T) {
	root := t.TempDir()
	drm := filepath.Join(root, "drm")
	writeGPUFixture(t, drm, "card1", "0x1002", "amdgpu", map[string]string{
		"mem_info_vram_used": "0", "mem_info_vram_total": "0",
	})
	gpu := NewLinuxSampler(filepath.Join(root, "missing"), drm).Sample().GPU
	if !gpu.Valid || !gpu.VRAM.Valid || gpu.VRAM.UsedBytes != 0 || gpu.VRAM.TotalBytes != 0 {
		t.Fatalf("readable zero heap became unavailable: %#v", gpu)
	}
}

func TestSamplerRejectsInvalidHostMemorySamples(t *testing.T) {
	for name, content := range map[string]string{
		"wrong unit":        "MemTotal: 1000 MB\nMemAvailable: 250 MB\n",
		"missing available": "MemTotal: 1000 kB\n",
		"available too big": "MemTotal: 1000 kB\nMemAvailable: 1001 kB\n",
		"overflow":          "MemTotal: 18446744073709551615 kB\nMemAvailable: 0 kB\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "meminfo")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if ram := NewLinuxSampler(path, filepath.Join(t.TempDir(), "drm")).Sample().RAM; ram.Valid {
				t.Fatalf("invalid sample became available: %#v", ram)
			}
		})
	}
}

func TestSamplerReadsMeasuredHostMemory(t *testing.T) {
	root := t.TempDir()
	meminfo := filepath.Join(root, "meminfo")
	if err := os.WriteFile(meminfo, []byte("MemTotal:       1000 kB\nMemAvailable:    250 kB\nMemFree:         100 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot := NewLinuxSampler(meminfo, filepath.Join(root, "drm")).Sample()
	if !snapshot.RAM.Valid {
		t.Fatalf("RAM sample unavailable: %#v", snapshot.RAM)
	}
	if snapshot.RAM.TotalBytes != 1000*1024 || snapshot.RAM.AvailableBytes != 250*1024 || snapshot.RAM.UsedBytes != 750*1024 {
		t.Fatalf("RAM sample = %#v", snapshot.RAM)
	}
}
