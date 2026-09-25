package commands

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/canonical/lscompute/pkg/machine"
	"github.com/canonical/lscompute/pkg/machine/cpu"
	"github.com/canonical/lscompute/pkg/machine/device/apusys"
	"github.com/canonical/lscompute/pkg/machine/device/fastrpc"
	"github.com/canonical/lscompute/pkg/machine/device/pci"
	"github.com/canonical/lscompute/pkg/machine/device/usb"
	"github.com/canonical/lscompute/pkg/machine/disk"
	"github.com/canonical/lscompute/pkg/machine/memory"
	"go.yaml.in/yaml/v4"
)

// hardwareInfoFixture returns a small, hand-built MachineInfo fixture for the named machine.
func hardwareInfoFixture(name string) (*machine.Machine, error) {
	switch name {
	case "dummy-machine":
		return &machine.Machine{
			CPUs: []cpu.CPU{{
				Architecture:   "amd64",
				ManufacturerId: "GenuineIntel",
				Flags:          []string{"fpu", "vme", "de"},
			}},
			Memory: memory.Memory{TotalRam: 67012501504, TotalSwap: 0},
			Disk: []disk.Disk{{
				Path:      "/var/lib/snapd/snaps",
				Total:     1006451294208,
				Available: 943543738368,
			}},
			PCIDevices: []pci.Device{{
				Bus:                  "pci",
				Slot:                 "0000:00:00.0",
				BusNumber:            0x0,
				DeviceClass:          0x600,
				ProgrammingInterface: new(uint8(0)),
				VendorId:             0x8086,
				DeviceId:             0x4637,
				SubvendorId:          new(uint16(0x103C)),
				SubdeviceId:          new(uint16(0x89C6)),
				AdditionalProperties: map[string]string{
					"compute-capability": "7.5",
					"vram":               "16000000",
					"microarchitecture":  "gfx1010",
				},
				FriendlyNames: pci.FriendlyNames{
					VendorName:    "Intel Corporation",
					SubvendorName: "Hewlett-Packard Company",
				},
			}},
			USBDevices: []usb.Device{
				{
					Bus:          "usb",
					BusNumber:    1,
					DeviceNumber: 1,
					VendorId:     0x1234,
					ProductId:    0x5678,
					FriendlyNames: usb.FriendlyNames{
						VendorName:  "Example Vendor",
						ProductName: "Example Product",
					},
				},
			},
			APUSYSDevices: []apusys.Device{
				{
					Bus:        "apusys",
					VendorName: "Example APUSYS Vendor",
				},
			},
			FastRPCDevices: []fastrpc.Device{
				{
					Bus: "fastrpc",
				},
			},
		}, nil

	default:
		return nil, fmt.Errorf("no machine fixture for %q", name)
	}
}

func TestHexInt_marshaling(t *testing.T) {
	value := hexInt(0xd0c)

	jsonValue, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(jsonValue) != `"0xd0c"` {
		t.Errorf("expected JSON hexadecimal string, got %s", jsonValue)
	}

	yamlValue, err := value.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if yamlValue != "0xd0c" {
		t.Errorf("expected YAML hexadecimal string, got %v", yamlValue)
	}
}

func TestCpuDetailsVerbose_marshaling(t *testing.T) {
	tests := []struct {
		name        string
		cpu         cpuDetailsVerbose
		wantJSON    string
		wantYAML    string
		wantYAMLErr bool
	}{
		{
			name: "AMD",
			cpu: cpuDetailsVerbose{
				Architecture:   cpu.Amd64,
				ManufacturerId: "AuthenticAMD",
			},
			wantJSON: `{"architecture":"amd64","manufacturer-id":"AuthenticAMD"}`,
			wantYAML: "architecture: amd64\nmanufacturer-id: AuthenticAMD\n",
		},
		{
			name: "ARM",
			cpu: cpuDetailsVerbose{
				Architecture:  cpu.Arm64,
				ImplementerId: hexInt(0x41),
			},
			wantJSON: `{"architecture":"arm64","implementer-id":"0x41"}`,
			wantYAML: "architecture: arm64\nimplementer-id: \"0x41\"\n",
		},
		{
			name: "RISCV64",
			cpu: cpuDetailsVerbose{
				Architecture:  cpu.Riscv64,
				ImplementerId: hexInt(0x41),
			},
			wantJSON: `{"architecture":"riscv64","implementer-id":"0x41"}`,
			wantYAML: "architecture: riscv64\nimplementer-id: \"0x41\"\n",
		},
		{
			name: "verbose",
			cpu: cpuDetailsVerbose{
				Architecture:   cpu.Amd64,
				ManufacturerId: "GenuineIntel",
			},
			wantJSON: `{"architecture":"amd64","manufacturer-id":"GenuineIntel"}`,
			wantYAML: "architecture: amd64\nmanufacturer-id: GenuineIntel\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.cpu)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.wantJSON {
				t.Errorf("expected %q, got %s", test.wantJSON, got)
			}
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := yaml.Marshal(test.cpu)
			if test.wantYAMLErr {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.wantYAML {
				t.Errorf("expected %q, got %s", test.wantYAML, got)
			}
		})
	}
}

func TestMemoryDetailsVerbose_marshaling(t *testing.T) {
	memoryZeroSwap := memoryDetailsVerbose{TotalRam: 8160437862, TotalSwap: 0}
	memorySwap := memoryDetailsVerbose{TotalRam: 8160437862, TotalSwap: 1000000000}

	got, err := yaml.Marshal(memoryZeroSwap)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "total-ram: 7.6G\ntotal-swap: 0\n" {
		t.Errorf("expected zero swap, got %q", got)
	}
	got, err = yaml.Marshal(memorySwap)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "total-ram: 7.6G\ntotal-swap: 953.7M\n" {
		t.Errorf("expected non-zero swap, got %q", got)
	}
	got, err = json.Marshal(memoryZeroSwap)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"total-ram":8160437862,"total-swap":0}` {
		t.Errorf("expected JSON for zero swap, got %q", got)
	}
	got, err = json.Marshal(memorySwap)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"total-ram":8160437862,"total-swap":1000000000}` {
		t.Errorf("expected JSON for non-zero swap, got %q", got)
	}
}

func TestDiskDetailsVerbose_marshaling(t *testing.T) {
	disk1 := diskDetailsVerbose{Path: "/var/lib/snapd/snaps", MountPoint: new("/"), Total: 1000000000000, Avail: 5000000000}
	disk2 := diskDetailsVerbose{Path: "/home", Total: 500000000000, MountPoint: new("/"), Avail: 5000000000}

	got, err := yaml.Marshal(disk1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "mount-point: /\npath: /var/lib/snapd/snaps\ntotal: 931.3G\navail: 4.7G\n" {
		t.Errorf("expected YAML for disk, got %q", got)
	}
	got, err = json.Marshal(disk1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"mount-point":"/","path":"/var/lib/snapd/snaps","total":1000000000000,"avail":5000000000}` {
		t.Errorf("expected JSON for disk, got %q", got)
	}
	got, err = yaml.Marshal(disk2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "mount-point: /\npath: /home\ntotal: 465.7G\navail: 4.7G\n" {
		t.Errorf("expected YAML for disk2, got %q", got)
	}
	got, err = json.Marshal(disk2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"mount-point":"/","path":"/home","total":500000000000,"avail":5000000000}` {
		t.Errorf("expected JSON for disk2, got %q", got)
	}
}

func TestPciDeviceDetailsVerbose_marshaling(t *testing.T) {
	p := pciDeviceDetailsVerbose{
		Bus:           "pci",
		VendorName:    "NVIDIA Corporation",
		DeviceName:    "GA102GL [RTX A5000]",
		SubvendorName: "NVIDIA Corporation",
		SubdeviceName: "RTX A5000",
		AdditionalProperties: &pciAdditionalDeviceProperties{
			Vram: 24 * 1024 * 1024 * 1024,
		},
	}
	pNoAddProps := pciDeviceDetailsVerbose{
		VendorName: "NVIDIA Corporation",
		DeviceName: "GA102GL [RTX A5000]",
	}

	got, err := yaml.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "bus: pci\nvendor-name: NVIDIA Corporation\ndevice-name: GA102GL [RTX A5000]\nsubvendor-name: NVIDIA Corporation\nsubdevice-name: RTX A5000\nadditional-properties:\n    vram: 24.0G\n" {
		t.Errorf("expected YAML for PCI device, got %q", got)
	}
	got, err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\"bus\":\"pci\",\"vendor-name\":\"NVIDIA Corporation\",\"device-name\":\"GA102GL [RTX A5000]\",\"subvendor-name\":\"NVIDIA Corporation\",\"subdevice-name\":\"RTX A5000\",\"additional-properties\":{\"vram\":25769803776}}" {
		t.Errorf("expected JSON for PCI device, got %q", got)
	}
	got, err = yaml.Marshal(pNoAddProps)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "bus: \"\"\nvendor-name: NVIDIA Corporation\ndevice-name: GA102GL [RTX A5000]\n" {
		t.Errorf("expected YAML for PCI device, got %q", got)
	}
	got, err = json.Marshal(pNoAddProps)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"bus":"","vendor-name":"NVIDIA Corporation","device-name":"GA102GL [RTX A5000]"}` {
		t.Errorf("expected compact JSON for PCI device, got %q", got)
	}

}
func TestApusysDeviceDetailsVerbose_marshaling(t *testing.T) {
	tests := []struct {
		name     string
		device   apuSysDeviceDetailsVerbose
		wantJSON string
		wantYAML string
	}{
		{
			name:     "compact",
			device:   apuSysDeviceDetailsVerbose{Bus: "apusys", VendorName: "MediaTek"},
			wantJSON: `{"bus":"apusys","vendor-name":"MediaTek"}`,
			wantYAML: "bus: apusys\nvendor-name: MediaTek\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jsonValue, err := json.Marshal(test.device)
			if err != nil {
				t.Fatal(err)
			}
			if string(jsonValue) != test.wantJSON {
				t.Errorf("expected JSON %q, got %s", test.wantJSON, jsonValue)
			}

			yamlValue, err := yaml.Marshal(test.device)
			if err != nil {
				t.Fatal(err)
			}
			if string(yamlValue) != test.wantYAML {
				t.Errorf("expected YAML %q, got %q", test.wantYAML, yamlValue)
			}
		})
	}
}

func Example_hardwareCommand_printMachineInfo_json() {
	cmd := hardwareCommand{format: "json", verbose: true}
	info, err := hardwareInfoFixture("dummy-machine")
	if err != nil {
		panic(err)
	}
	details := *cmd.newHardwareDetails(info)

	if err := cmd.printHardwareInfo(details); err != nil {
		panic(err)
	}

	// Output:
	// {
	//   "cpus": [
	//     {
	//       "architecture": "amd64",
	//       "manufacturer-id": "GenuineIntel"
	//     }
	//   ],
	//   "accelerators": [
	//     {
	//       "bus": "pci",
	//       "vendor-name": "Intel Corporation",
	//       "subvendor-name": "Hewlett-Packard Company",
	//       "additional-properties": {
	//         "microarchitecture": "gfx1010",
	//         "vram": 16000000,
	//         "compute-capability": "7.5"
	//       }
	//     },
	//     {
	//       "bus": "usb",
	//       "vendor-name": "Example Vendor",
	//       "product-name": "Example Product"
	//     },
	//     {
	//       "bus": "fastrpc"
	//     },
	//     {
	//       "bus": "apusys",
	//       "vendor-name": "Example APUSYS Vendor"
	//     }
	//   ],
	//   "memory": {
	//     "total-ram": 67012501504,
	//     "total-swap": 0
	//   },
	//   "disks": [
	//     {
	//       "path": "/var/lib/snapd/snaps",
	//       "total": 1006451294208,
	//       "avail": 943543738368
	//     }
	//   ]
	// }

}

func Example_hardwareCommand_printMachineInfo_plain() {
	cmd := hardwareCommand{format: "plain", verbose: true}
	info, err := hardwareInfoFixture("dummy-machine")
	if err != nil {
		panic(err)
	}
	details := *cmd.newHardwareDetails(info)

	if err := cmd.printHardwareInfo(details); err != nil {
		panic(err)
	}

	// Output:
	// cpus:
	//     - architecture: amd64
	//       manufacturer-id: GenuineIntel
	// accelerators:
	//     - bus: pci
	//       vendor-name: Intel Corporation
	//       subvendor-name: Hewlett-Packard Company
	//       additional-properties:
	//         microarchitecture: gfx1010
	//         vram: 15.3M
	//         compute-capability: "7.5"
	//     - bus: usb
	//       vendor-name: Example Vendor
	//       product-name: Example Product
	//     - bus: fastrpc
	//     - bus: apusys
	//       vendor-name: Example APUSYS Vendor
	// memory:
	//     total-ram: 62.4G
	//     total-swap: 0
	// disks:
	//     - path: /var/lib/snapd/snaps
	//       total: 937.3G
	//       avail: 878.7G
}

func Example_hardwareCommand_printMachineInfo_plainCompact() {
	cmd := hardwareCommand{format: "plain", verbose: false}
	info, err := hardwareInfoFixture("dummy-machine")
	if err != nil {
		panic(err)
	}
	details := *cmd.newHardwareDetails(info)

	if err := cmd.printHardwareInfo(details); err != nil {
		panic(err)
	}

	// Output:
	// cpus:
	//     - amd64 (GenuineIntel)
	// accelerators:
	//     - Intel Corporation (VRAM 15.3M)
	//     - Example Vendor Example Product
	//     - fastrpc
	//     - Example APUSYS Vendor
	// memory: 62.4G (Swap 0)
	// disks:
	//     - /var/lib/snapd/snaps (Free 878.7G / 937.3G)
}

func Test_printHardwareInfo_unknownFormat(t *testing.T) {
	cmd := hardwareCommand{format: "xml"}
	info := hardwareDetailsVerbose{}

	err := cmd.printHardwareInfo(info)
	if err == nil || err.Error() != `unknown format "xml"` {
		t.Errorf("expected error 'unknown format \"xml\"', got %v", err)
	}
}

func Test_compactCpus(t *testing.T) {
	cmd := hardwareCommand{}
	cpus := []cpu.CPU{
		{Architecture: cpu.Amd64, ManufacturerId: "GenuineIntel"},
		{Architecture: cpu.Amd64, ManufacturerId: "GenuineIntel"},
		{Architecture: cpu.Arm64, ManufacturerId: "ARM"},
	}

	compact := cmd.compactCPUs(cpus)
	if len(compact) != 2 {
		t.Errorf("expected 2 compacted CPUs, got %d", len(compact))
	}
	if compact[0].Architecture != cpu.Amd64 || compact[1].Architecture != cpu.Arm64 {
		t.Errorf("unexpected compacted CPU architectures: %+v", compact)
	}
}
