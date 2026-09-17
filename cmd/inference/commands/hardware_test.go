package commands

import (
	"encoding/json"
	"testing"

	"github.com/canonical/lscompute/pkg/machine"
	"github.com/canonical/lscompute/pkg/machine/cpu"
	"gopkg.in/yaml.v3"
)

func TestCpuDetails_compactName(t *testing.T) {
	emptyModelName := ""
	modelName := "Ampere Altra Max"
	tests := []struct {
		name string
		cpu  CpuDetails
		want string
	}{
		{
			name: "model name takes precedence",
			cpu: CpuDetails{
				ModelName:   &modelName,
				BrandString: "fallback brand",
				Processor:   128,
			},
			want: "Ampere Altra Max 128 threads",
		},
		{
			name: "brand when model name is empty",
			cpu: CpuDetails{
				ModelName:   &emptyModelName,
				BrandString: "Cortex-A78",
				Processor:   4,
			},
			want: "Cortex-A78 4 threads",
		},
		{
			name: "manufacturer and architecture when names are empty",
			cpu: CpuDetails{
				Architecture:   "arm64",
				ManufacturerId: "mediatek",
				Processor:      4,
			},
			want: "mediatek arm64 4 threads",
		},
		{
			name: "ARM implementer and part lookup",
			cpu: CpuDetails{
				Architecture:  "arm64",
				ImplementerId: 0x41,
				PartNumber:    0xd0c,
				Processor:     128,
			},
			want: "Neoverse-N1 128 threads",
		},
		{
			name: "known ARM implementer with unknown part",
			cpu: CpuDetails{
				Architecture:  "arm64",
				ImplementerId: 0x41,
				PartNumber:    0x123,
				Processor:     8,
			},
			want: "ARM arm64 (part 0x123) 8 threads",
		},
		{
			name: "unknown ARM implementer",
			cpu: CpuDetails{
				Architecture:  "arm64",
				ImplementerId: 0xff,
				PartNumber:    0x123,
				Processor:     8,
			},
			want: "arm64 (implementer 0xff, part 0x123) 8 threads",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.cpu)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != `"`+test.want+`"` {
				t.Errorf("expected %q, got %s", test.want, got)
			}
		})
	}
}

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
		FriendlyNames: cpu.FriendlyNames{Threads: 128},
	}}}

	details := cmd.newMachineDetails(info)
	got, err := json.Marshal(details.CPUs[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `"Neoverse-N1 128 threads"` {
		t.Errorf("expected resolved ARM CPU, got %s", got)
	}
}

func TestMemoryDetails_compactZeroSwap(t *testing.T) {
	memory := MemoryDetails{TotalRam: 8160437862}

	got, err := yaml.Marshal(memory)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "7.6G (Swap 0)\n" {
		t.Errorf("expected zero swap, got %q", got)
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
	//       "total": "937.3G",
	//       "avail": "878.7G"
	//     }
	//   ],
	//   "accelerators": [
	//     {
	//       "bus": "pci",
	//       "vendor-name": "Intel Corporation",
	//       "subvendor-name": "Hewlett-Packard Company",
	//       "additional-properties": {
	//         "microarchitecture": "gfx1010",
	//         "vram": 0,
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
	//         vram: 0
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
	//     "GenuineIntel amd64 0 threads"
	//   ],
	//   "memory": "62.4G (Swap 0)",
	//   "disks": [
	//     "/var/lib/snapd/snaps (Free 878.7G / 937.3G)"
	//   ],
	//   "accelerators": [
	//     "Intel Corporation (VRAM: 0)",
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
	//     - GenuineIntel amd64 0 threads
	// accelerators:
	//     - 'Intel Corporation (VRAM: 0)'
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
