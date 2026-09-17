package commands

import (
	"fmt"

	"github.com/canonical/lscompute/pkg/machine"
	"github.com/canonical/lscompute/pkg/machine/cpu"
	"github.com/canonical/lscompute/pkg/machine/device/pci"
	"github.com/canonical/lscompute/pkg/machine/device/usb"
	"github.com/canonical/lscompute/pkg/machine/disk"
	"github.com/canonical/lscompute/pkg/machine/memory"
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
		}, nil

	default:
		return nil, fmt.Errorf("no machine fixture for %q", name)
	}
}
