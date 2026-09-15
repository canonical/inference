package commands

import (
	"testing"
)

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

func Example_hardwareCommand_printMachineInfo_yaml() {
	cmd := hardwareCommand{format: "yaml", verbose: true}
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
