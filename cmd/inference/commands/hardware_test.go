package commands

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCpuDetails_compactName(t *testing.T) {
	emptyModelName := ""
	tests := []struct {
		name string
		cpu  CpuDetails
		want string
	}{
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

func Test_printMachineInfo_unknownFormat(t *testing.T) {
	cmd := hardwareCommand{format: "xml"}
	info := MachineDetails{}

	err := cmd.printMachineInfo(info)
	if err == nil || err.Error() != `unknown format "xml"` {
		t.Errorf("expected error 'unknown format \"xml\"', got %v", err)
	}
}
