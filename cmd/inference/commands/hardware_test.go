package commands

import (
	"encoding/json"
	"testing"

	"github.com/canonical/lscompute/pkg/machine"
	"github.com/canonical/lscompute/pkg/machine/cpu"
	"gopkg.in/yaml.v3"
)

func TestHexInt_marshaling(t *testing.T) {
	value := HexInt(0xd0c)

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

func TestCpuDetails_marshaling(t *testing.T) {
	tests := []struct {
		name    string
		cpu     CpuDetails
		want    string
		wantErr bool
	}{
		{
			name: "AMD",
			cpu: CpuDetails{
				Architecture:   cpu.Amd64,
				ManufacturerId: "AuthenticAMD",
			},
			want: "AuthenticAMD amd64",
		},
		{
			name: "ARM",
			cpu: CpuDetails{
				Architecture:  cpu.Arm64,
				ImplementerId: HexInt(0x41),
			},
			want: "arm64",
		},
		{
			name: "RISCV64",
			cpu: CpuDetails{
				Architecture:  cpu.Riscv64,
				ImplementerId: HexInt(0x41),
			},
			want: "riscv64",
		},
		{
			name: "unknown arch implementer",
			cpu: CpuDetails{
				Architecture: cpu.Ppc64,
			},
			want:    "unsupported architecture: ppc64",
			wantErr: true,
		},
		{
			name: "verbose",
			cpu: CpuDetails{
				Verbose:        true,
				Architecture:   cpu.Amd64,
				ManufacturerId: "GenuineIntel",
			},
			want: `{"architecture":"amd64","manufacturer-id":"GenuineIntel"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.cpu)
			if test.wantErr {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := `"` + test.want + `"`
			if test.cpu.Verbose {
				want = test.want
			}
			if string(got) != want {
				t.Errorf("expected %q, got %s", test.want, got)
			}
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := yaml.Marshal(test.cpu)
			if test.wantErr {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := test.want + "\n"
			if test.cpu.Verbose {
				want = "architecture: amd64\nmanufacturer-id: GenuineIntel\n"
			}
			if string(got) != want {
				t.Errorf("expected %q, got %s", test.want, got)
			}
		})
	}
}

func TestMemoryDetails_marshaling(t *testing.T) {
	memoryZeroSwap := MemoryDetails{TotalRam: 8160437862, TotalSwap: 0}
	memorySwap := MemoryDetails{TotalRam: 8160437862, TotalSwap: 1000000000, Verbose: true}

	got, err := yaml.Marshal(memoryZeroSwap)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "7.6G (Swap 0)\n" {
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
	if string(got) != `"7.6G (Swap 0)"` {
		t.Errorf("expected JSON for zero swap, got %q", got)
	}
	got, err = json.Marshal(memorySwap)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"total-ram":"7.6G","total-swap":"953.7M"}` {
		t.Errorf("expected JSON for non-zero swap, got %q", got)
	}
}

func TestDiskDetails_marshaling(t *testing.T) {
	disk1 := DiskDetails{Path: "/var/lib/snapd/snaps", MountPoint: new("/"), Total: 1000000000000, Avail: 5000000000}
	disk2 := DiskDetails{Path: "/home", Total: 500000000000, MountPoint: new("/"), Avail: 5000000000, Verbose: true}

	got, err := yaml.Marshal(disk1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "/ (Free 4.7G / 931.3G)\n" {
		t.Errorf("expected YAML for disk, got %q", got)
	}
	got, err = json.Marshal(disk1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `"/ (Free 5000000000 / 1000000000000)"` {
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

func TestPciDeviceDetails_marshaling(t *testing.T) {
	compact := PciDeviceDetails{
		VendorName: "NVIDIA Corporation",
		DeviceName: "GA102GL [RTX A5000]",
		AdditionalProperties: &PciAdditionalDeviceProperties{
			Vram: 24 * 1024 * 1024 * 1024,
		},
	}
	compactNoAddProps := PciDeviceDetails{
		VendorName: "NVIDIA Corporation",
		DeviceName: "GA102GL [RTX A5000]",
	}
	verbose := PciDeviceDetails{
		Bus:           "pci",
		VendorName:    "NVIDIA Corporation",
		DeviceName:    "GA102GL [RTX A5000]",
		SubvendorName: "NVIDIA Corporation",
		SubdeviceName: "RTX A5000",
		Verbose:       true,
	}

	got, err := yaml.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NVIDIA Corporation GA102GL [RTX A5000] (VRAM 24.0G)\n" {
		t.Errorf("expected compact YAML for PCI device, got %q", got)
	}
	got, err = json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `"NVIDIA Corporation GA102GL [RTX A5000] (VRAM 24.0G)"` {
		t.Errorf("expected compact JSON for PCI device, got %q", got)
	}
	got, err = yaml.Marshal(compactNoAddProps)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NVIDIA Corporation GA102GL [RTX A5000]\n" {
		t.Errorf("expected compact YAML for PCI device, got %q", got)
	}
	got, err = json.Marshal(compactNoAddProps)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `"NVIDIA Corporation GA102GL [RTX A5000]"` {
		t.Errorf("expected compact JSON for PCI device, got %q", got)
	}
	got, err = yaml.Marshal(verbose)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "bus: pci\nvendor-name: NVIDIA Corporation\ndevice-name: GA102GL [RTX A5000]\nsubvendor-name: NVIDIA Corporation\nsubdevice-name: RTX A5000\n" {
		t.Errorf("expected verbose YAML for PCI device, got %q", got)
	}
	got, err = json.Marshal(verbose)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"bus":"pci","vendor-name":"NVIDIA Corporation","device-name":"GA102GL [RTX A5000]","subvendor-name":"NVIDIA Corporation","subdevice-name":"RTX A5000"}` {
		t.Errorf("expected verbose JSON for PCI device, got %q", got)
	}

}
func TestApusysDeviceDetails_marshaling(t *testing.T) {
	tests := []struct {
		name     string
		device   ApusysDeviceDetails
		wantJSON string
		wantYAML string
	}{
		{
			name:     "compact",
			device:   ApusysDeviceDetails{Bus: "apusys", VendorName: "MediaTek"},
			wantJSON: `"MediaTek"`,
			wantYAML: "MediaTek\n",
		},
		{
			name:     "verbose",
			device:   ApusysDeviceDetails{Bus: "apusys", VendorName: "MediaTek", Verbose: true},
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

func TestHardwareCommand_newMachineDetails_resolvesARMCPU(t *testing.T) {
	cmd := hardwareCommand{}
	info := &machine.Machine{CPUs: []cpu.CPU{{
		Architecture:  "arm64",
		ImplementerId: 0x41,
		PartNumber:    0xd0c,
	}}}

	details := cmd.newMachineDetails(info)
	got, err := json.Marshal(details.CPUs[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `"arm64"` {
		t.Errorf("expected resolved arm64, got %s", got)
	}
}

func Example_hardwareCommand_printMachineInfo_json() {
	cmd := hardwareCommand{format: "json", verbose: true}
	info, err := hardwareInfoFixture("dummy-machine")
	if err != nil {
		panic(err)
	}
	details := *cmd.newMachineDetails(info)

	if err := cmd.printMachineInfo(details); err != nil {
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
	//   "memory": {
	//     "total-ram": "62.4G",
	//     "total-swap": 0
	//   },
	//   "disks": [
	//     {
	//       "path": "/var/lib/snapd/snaps",
	//       "total": 1006451294208,
	//       "avail": 943543738368
	//     }
	//   ],
	//   "accelerators": [
	//     {
	//       "bus": "pci",
	//       "vendor-name": "Intel Corporation",
	//       "subvendor-name": "Hewlett-Packard Company",
	//       "additional-properties": {
	//         "microarchitecture": "gfx1010",
	//         "vram": "15.3M",
	//         "compute-capability": "7.5"
	//       }
	//     },
	//     {
	//       "bus": "usb",
	//       "vendor-name": "Example Vendor",
	//       "product-name": "Example Product"
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
	details := *cmd.newMachineDetails(info)

	if err := cmd.printMachineInfo(details); err != nil {
		panic(err)
	}

	// Output:
	// cpus:
	//     - architecture: amd64
	//       manufacturer-id: GenuineIntel
	// memory:
	//     total-ram: 62.4G
	//     total-swap: 0
	// disks:
	//     - path: /var/lib/snapd/snaps
	//       total: 937.3G
	//       avail: 878.7G
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
}

func Example_hardwareCommand_printMachineInfo_jsonCompact() {
	cmd := hardwareCommand{format: "json"}
	info, err := hardwareInfoFixture("dummy-machine")
	if err != nil {
		panic(err)
	}
	details := *cmd.newMachineDetails(info)

	if err := cmd.printMachineInfo(details); err != nil {
		panic(err)
	}

	// Output:
	// {
	//   "cpus": [
	//     "GenuineIntel amd64"
	//   ],
	//   "memory": "62.4G (Swap 0)",
	//   "disks": [
	//     "/var/lib/snapd/snaps (Free 943543738368 / 1006451294208)"
	//   ],
	//   "accelerators": [
	//     "Intel Corporation (VRAM 15.3M)",
	//     {
	//       "bus": "usb",
	//       "vendor-name": "Example Vendor",
	//       "product-name": "Example Product"
	//     }
	//   ]
	// }
}

func Example_hardwareCommand_printMachineInfo_plainCompact() {
	cmd := hardwareCommand{format: "plain"}
	info, err := hardwareInfoFixture("dummy-machine")
	if err != nil {
		panic(err)
	}
	details := *cmd.newMachineDetails(info)

	if err := cmd.printMachineInfo(details); err != nil {
		panic(err)
	}

	// Output:
	// cpus:
	//     - GenuineIntel amd64
	// accelerators:
	//     - Intel Corporation (VRAM 15.3M)
	//     - bus: usb
	//       vendor-name: Example Vendor
	//       product-name: Example Product
	// memory: 62.4G (Swap 0)
	// disks:
	//     - /var/lib/snapd/snaps (Free 878.7G / 937.3G)
}

func Test_printMachineInfo_unknownFormat(t *testing.T) {
	cmd := hardwareCommand{format: "xml"}
	info := MachineDetails{}

	err := cmd.printMachineInfo(info)
	if err == nil || err.Error() != `unknown format "xml"` {
		t.Errorf("expected error 'unknown format \"xml\"', got %v", err)
	}
}
