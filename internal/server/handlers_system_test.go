package server

import "testing"

func TestParseNVIDIASMIOutput(t *testing.T) {
	out := []byte("NVIDIA GeForce RTX 4090, 1536, 24564\n")

	info, ok := parseNVIDIASMIOutput(out)
	if !ok {
		t.Fatal("parseNVIDIASMIOutput returned false, want true")
	}
	if info.Name != "NVIDIA GeForce RTX 4090" {
		t.Fatalf("unexpected GPU name: %q", info.Name)
	}
	if info.VRAMUsed != 1536*1024*1024 {
		t.Fatalf("unexpected GPU used bytes: %d", info.VRAMUsed)
	}
	if info.VRAMTotal != 24564*1024*1024 {
		t.Fatalf("unexpected GPU total bytes: %d", info.VRAMTotal)
	}
	if !info.UsageAvailable {
		t.Fatal("UsageAvailable = false, want true")
	}
	if info.SharedMemory {
		t.Fatal("SharedMemory = true, want false")
	}
}

func TestParseNVIDIASMIOutputGB10UnifiedMemory(t *testing.T) {
	out := []byte("NVIDIA GB10, Not Supported, Not Supported\n")

	info, ok := parseNVIDIASMIOutput(out)
	if !ok {
		t.Fatal("parseNVIDIASMIOutput returned false, want true")
	}
	if info.Name != "NVIDIA GB10" {
		t.Fatalf("unexpected GPU name: %q", info.Name)
	}
	if info.VRAMUsed != 0 || info.VRAMTotal != 0 {
		t.Fatalf("unexpected GPU memory values: used=%d total=%d", info.VRAMUsed, info.VRAMTotal)
	}
	if info.UsageAvailable {
		t.Fatal("UsageAvailable = true, want false")
	}
	if !info.SharedMemory {
		t.Fatal("SharedMemory = false, want true")
	}
}

func TestParseDarwinSystemProfilerOutputAppleSilicon(t *testing.T) {
	out := []byte(`{
  "SPDisplaysDataType" : [
    {
      "_name" : "Apple M2 Pro",
      "spdisplays_vendor" : "sppci_vendor_Apple",
      "sppci_device_type" : "spdisplays_gpu",
      "sppci_model" : "Apple M2 Pro"
    }
  ]
}`)

	name, total, shared := parseDarwinSystemProfilerOutput(out)
	if name != "Apple M2 Pro" {
		t.Fatalf("unexpected GPU name: %q", name)
	}
	if total != 0 {
		t.Fatalf("unexpected GPU total bytes: %d", total)
	}
	if !shared {
		t.Fatal("shared = false, want true")
	}
}

func TestParseDarwinSystemProfilerOutputDiscreteVRAM(t *testing.T) {
	out := []byte(`{
  "SPDisplaysDataType" : [
    {
      "_name" : "Radeon Pro 5500M",
      "spdisplays_vendor" : "sppci_vendor_AMD",
      "spdisplays_vram" : "4 GB",
      "sppci_model" : "Radeon Pro 5500M"
    }
  ]
}`)

	name, total, shared := parseDarwinSystemProfilerOutput(out)
	if name != "Radeon Pro 5500M" {
		t.Fatalf("unexpected GPU name: %q", name)
	}
	if total != 4*1024*1024*1024 {
		t.Fatalf("unexpected GPU total bytes: %d", total)
	}
	if shared {
		t.Fatal("shared = true, want false")
	}
}

func TestParseDarwinIORegGPUMemoryAppleSilicon(t *testing.T) {
	out := []byte(`      "PerformanceStatistics" = {"In use system memory (driver)"=0,"Alloc system memory"=1330167808,"Tiler Utilization %"=27,"Renderer Utilization %"=26,"Device Utilization %"=29,"In use system memory"=521256960}`)

	used, ok := parseDarwinIORegGPUMemory(out)
	if !ok {
		t.Fatal("parseDarwinIORegGPUMemory returned false, want true")
	}
	if used != 521256960 {
		t.Fatalf("unexpected GPU used bytes: %d", used)
	}
}

func TestParseDarwinIORegGPUMemoryIntelDiscrete(t *testing.T) {
	out := []byte(`      "PerformanceStatistics" = {"vramUsedBytes"=1073741824,"Device Utilization %"=12}`)

	used, ok := parseDarwinIORegGPUMemory(out)
	if !ok {
		t.Fatal("parseDarwinIORegGPUMemory returned false, want true")
	}
	if used != 1073741824 {
		t.Fatalf("unexpected GPU used bytes: %d", used)
	}
}

func TestParseDarwinIORegGPUMemoryNoData(t *testing.T) {
	if _, ok := parseDarwinIORegGPUMemory([]byte(`"IOClass" = "AGXAcceleratorG14X"`)); ok {
		t.Fatal("parseDarwinIORegGPUMemory returned true, want false")
	}
}

func TestParseMemoryString(t *testing.T) {
	if got := parseMemoryString("Dynamic, Max: 48 GB"); got != 48*1024*1024*1024 {
		t.Fatalf("parseMemoryString returned %d, want %d", got, 48*1024*1024*1024)
	}
}

func TestParseWindowsCPUInfoUsesTotalUsage(t *testing.T) {
	out := []byte(`{"Processors":[{"Name":"AMD Ryzen 7 8845HS","LoadPercentage":12,"MaxClockSpeed":3800}],"TotalUsage":37}`)

	usage, clock := parseWindowsCPUInfo(out)
	if usage != 37 {
		t.Fatalf("unexpected CPU usage: %v", usage)
	}
	if clock != "AMD Ryzen 7 8845HS" {
		t.Fatalf("unexpected CPU clock label: %q", clock)
	}
}

func TestParseWindowsCPUInfoFallsBackToProcessorAverage(t *testing.T) {
	out := []byte(`{"Processors":[{"Name":"","LoadPercentage":10,"MaxClockSpeed":3000},{"Name":"","LoadPercentage":20,"MaxClockSpeed":3000}],"TotalUsage":null}`)

	usage, clock := parseWindowsCPUInfo(out)
	if usage != 15 {
		t.Fatalf("unexpected fallback CPU usage: %v", usage)
	}
	if clock != "3.00 GHz" {
		t.Fatalf("unexpected fallback CPU clock label: %q", clock)
	}
}

func TestParseWindowsRAMInfo(t *testing.T) {
	out := []byte(`{"TotalVisibleMemorySize":16711400,"FreePhysicalMemory":6412340}`)

	used, total, info := parseWindowsRAMInfo(out)
	if total != 16711400*1024 {
		t.Fatalf("unexpected total memory: %d", total)
	}
	if used != (16711400-6412340)*1024 {
		t.Fatalf("unexpected used memory: %d", used)
	}
	if info != "" {
		t.Fatalf("unexpected RAM info label: %q", info)
	}
}

func TestParseWindowsGPUInfoIntegratedAMD(t *testing.T) {
	out := []byte(`{"Controllers":[{"Name":"AMD Radeon(TM) 780M Graphics","AdapterRAM":536870912,"PNPDeviceID":"PCI\\VEN_1002&DEV_15BF&SUBSYS_38031025&REV_C7\\3&11583659&2&08"}],"Registry":[{"MatchingDeviceId":"PCI\\VEN_1002&DEV_15BF","MemorySize":536870912}],"AdapterMemory":[{"DedicatedUsage":268435456,"SharedUsage":1073741824}]}`)
	systemRAM := uint64(64) * 1024 * 1024 * 1024

	info, ok := parseWindowsGPUInfo(out, systemRAM)
	if !ok {
		t.Fatal("parseWindowsGPUInfo returned false, want true")
	}
	if info.Name != "AMD Radeon(TM) 780M Graphics" {
		t.Fatalf("unexpected GPU name: %q", info.Name)
	}
	if !info.SharedMemory {
		t.Fatal("SharedMemory = false, want true")
	}
	if info.VRAMTotal != systemRAM/2 {
		t.Fatalf("unexpected GPU total bytes: %d", info.VRAMTotal)
	}
	if info.VRAMUsed != 268435456+1073741824 {
		t.Fatalf("unexpected GPU used bytes: %d", info.VRAMUsed)
	}
	if !info.UsageAvailable {
		t.Fatal("UsageAvailable = false, want true")
	}
}

func TestParseWindowsGPUInfoDiscreteRegistryOverridesAdapterRAM(t *testing.T) {
	// AdapterRAM is a uint32 and caps at 4 GB; the registry qwMemorySize
	// reports the real 16 GB.
	out := []byte(`{"Controllers":[{"Name":"AMD Radeon RX 7800 XT","AdapterRAM":4293918720,"PNPDeviceID":"PCI\\VEN_1002&DEV_747E&SUBSYS_53201849&REV_C8\\6&E22F79A&0&00000019"}],"Registry":[{"MatchingDeviceId":"PCI\\VEN_1002&DEV_747E","MemorySize":17163091968}],"AdapterMemory":[{"DedicatedUsage":2147483648,"SharedUsage":134217728}]}`)

	info, ok := parseWindowsGPUInfo(out, 32*1024*1024*1024)
	if !ok {
		t.Fatal("parseWindowsGPUInfo returned false, want true")
	}
	if info.SharedMemory {
		t.Fatal("SharedMemory = true, want false")
	}
	if info.VRAMTotal != 17163091968 {
		t.Fatalf("unexpected GPU total bytes: %d", info.VRAMTotal)
	}
	if info.VRAMUsed != 2147483648 {
		t.Fatalf("unexpected GPU used bytes: %d", info.VRAMUsed)
	}
}

func TestParseWindowsGPUInfoSkipsVirtualAdapters(t *testing.T) {
	out := []byte(`{"Controllers":[{"Name":"Microsoft Basic Display Adapter","AdapterRAM":null,"PNPDeviceID":""},{"Name":"Microsoft Hyper-V Video","AdapterRAM":null,"PNPDeviceID":""}],"Registry":[],"AdapterMemory":[]}`)

	if _, ok := parseWindowsGPUInfo(out, 16*1024*1024*1024); ok {
		t.Fatal("parseWindowsGPUInfo returned true, want false")
	}
}

func TestParseWindowsGPUInfoNoUsageCounters(t *testing.T) {
	out := []byte(`{"Controllers":[{"Name":"Intel(R) Arc(TM) A770 Graphics","AdapterRAM":4293918720,"PNPDeviceID":"PCI\\VEN_8086&DEV_56A0\\ABC"}],"Registry":[{"MatchingDeviceId":"PCI\\VEN_8086&DEV_56A0","MemorySize":17163091968}],"AdapterMemory":[]}`)

	info, ok := parseWindowsGPUInfo(out, 32*1024*1024*1024)
	if !ok {
		t.Fatal("parseWindowsGPUInfo returned false, want true")
	}
	if info.UsageAvailable {
		t.Fatal("UsageAvailable = true, want false")
	}
	if info.VRAMUsed != 0 {
		t.Fatalf("unexpected GPU used bytes: %d", info.VRAMUsed)
	}
	if info.VRAMTotal != 17163091968 {
		t.Fatalf("unexpected GPU total bytes: %d", info.VRAMTotal)
	}
}

func TestMemoryFieldUnsupported(t *testing.T) {
	cases := []string{"N/A", "Not Supported", "not supported"}
	for _, input := range cases {
		if !memoryFieldUnsupported(input) {
			t.Fatalf("memoryFieldUnsupported(%q) = false, want true", input)
		}
	}
}
