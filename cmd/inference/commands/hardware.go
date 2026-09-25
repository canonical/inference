package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/lscompute/pkg/machine"
	"github.com/canonical/lscompute/pkg/machine/cpu"
	"github.com/canonical/lscompute/pkg/machine/host"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"
)

type hexInt uint64

func (h hexInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(fmt.Sprintf("0x%x", uint64(h)))
}

func (h hexInt) MarshalYAML() (any, error) {
	return fmt.Sprintf("0x%x", uint64(h)), nil
}

type hardwareCommand struct {
	*common.Context

	// flags
	verbose bool
	format  string
}

type hardwareDetails struct {
	CPUs         []string `yaml:"cpus,omitempty"`
	Accelerators []string `yaml:"accelerators,omitempty"`
	Memory       string   `yaml:"memory,omitempty"`
	Disk         []string `yaml:"disks,omitempty"`
}

type hardwareDetailsVerbose struct {
	CPUs         []cpuDetailsVerbose  `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	Accelerators []any                `json:"accelerators,omitempty" yaml:"accelerators,omitempty"`
	Memory       memoryDetailsVerbose `json:"memory,omitempty" yaml:"memory,omitempty"`
	Disk         []diskDetailsVerbose `json:"disks,omitempty" yaml:"disks,omitempty"`
}

type cpuDetailsVerbose struct {
	Architecture   string `json:"architecture" yaml:"architecture"`
	ManufacturerId string `json:"manufacturer-id,omitempty" yaml:"manufacturer-id,omitempty"`
	ImplementerId  hexInt `json:"implementer-id,omitempty" yaml:"implementer-id,omitempty"`
}

type memoryDetailsVerbose struct {
	TotalRam  uint64 `json:"total-ram" yaml:"total-ram"`
	TotalSwap uint64 `json:"total-swap" yaml:"total-swap"`
}

func (m memoryDetailsVerbose) MarshalYAML() (any, error) {
	return struct {
		TotalRam  any `yaml:"total-ram"`
		TotalSwap any `yaml:"total-swap"`
	}{
		TotalRam:  common.FormatBytes(m.TotalRam),
		TotalSwap: common.FormatBytes(m.TotalSwap),
	}, nil
}

type diskDetailsVerbose struct {
	MountPoint *string `json:"mount-point,omitempty" yaml:"mount-point,omitempty"`
	Path       string  `json:"path" yaml:"path"`
	Total      uint64  `json:"total" yaml:"total"`
	Avail      uint64  `json:"avail" yaml:"avail"`
}

func (d diskDetailsVerbose) MarshalYAML() (any, error) {
	return struct {
		MountPoint *string `yaml:"mount-point,omitempty"`
		Path       string  `yaml:"path"`
		Total      any     `yaml:"total"`
		Avail      any     `yaml:"avail"`
	}{
		MountPoint: d.MountPoint,
		Path:       d.Path,
		Total:      common.FormatBytes(d.Total),
		Avail:      common.FormatBytes(d.Avail),
	}, nil
}

type pciDeviceDetailsVerbose struct {
	Bus                  string                         `json:"bus" yaml:"bus"`
	VendorName           string                         `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	DeviceName           string                         `json:"device-name,omitempty" yaml:"device-name,omitempty"`
	SubvendorName        string                         `json:"subvendor-name,omitempty" yaml:"subvendor-name,omitempty"`
	SubdeviceName        string                         `json:"subdevice-name,omitempty" yaml:"subdevice-name,omitempty"`
	AdditionalProperties *pciAdditionalDeviceProperties `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
}

func (p pciDeviceDetailsVerbose) compactName() string {
	name := strings.TrimSpace(fmt.Sprintf("%s %s", p.VendorName, p.DeviceName))
	if p.AdditionalProperties == nil {
		return name
	}
	return fmt.Sprintf("%s (VRAM %v)", name, common.FormatBytes(p.AdditionalProperties.Vram))
}

type pciAdditionalDeviceProperties struct {
	Microarchitecture string `json:"microarchitecture,omitempty" yaml:"microarchitecture,omitempty"`
	Vram              uint64 `json:"vram,omitempty" yaml:"vram,omitempty"`
	ComputeCapability string `json:"compute-capability,omitempty" yaml:"compute-capability,omitempty"`
}

func (a pciAdditionalDeviceProperties) MarshalYAML() (any, error) {
	return struct {
		Microarchitecture string `yaml:"microarchitecture,omitempty"`
		Vram              any    `yaml:"vram,omitempty"`
		ComputeCapability string `yaml:"compute-capability,omitempty"`
	}{
		Microarchitecture: a.Microarchitecture,
		Vram:              common.FormatBytes(a.Vram),
		ComputeCapability: a.ComputeCapability,
	}, nil
}

type usbDeviceDetailsVerbose struct {
	Bus                  string            `json:"bus" yaml:"bus"`
	VendorName           string            `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	ProductName          string            `json:"product-name,omitempty" yaml:"product-name,omitempty"`
	AdditionalProperties map[string]string `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
}

func (u usbDeviceDetailsVerbose) compactName() string {
	return strings.TrimSpace(fmt.Sprintf("%s %s", u.VendorName, u.ProductName))
}

type fastRPCDeviceDetailsVerbose struct {
	Bus                  string            `json:"bus" yaml:"bus"`
	AdditionalProperties map[string]string `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
}

type apuSysDeviceDetailsVerbose struct {
	Bus        string `json:"bus" yaml:"bus"`
	VendorName string `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
}

func Hardware(ctx *common.Context) *cobra.Command {
	var cmd hardwareCommand
	cmd.Context = ctx

	cobraCmd := &cobra.Command{
		Use:               "hardware",
		Short:             "Print information about the host hardware",
		Long:              "Print information about the host hardware, including hardware and compute resources",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE:              cmd.run,
	}

	// flags
	supportedFormats := []string{"json", "plain"}
	cobraCmd.Flags().StringVar(
		&cmd.format,
		"format",
		"plain",
		fmt.Sprintf("output format [%s]", strings.Join(supportedFormats, "|")),
	)
	cobraCmd.Flags().BoolVar(
		&cmd.verbose,
		"verbose",
		false,
		"enable verbose output",
	)

	return cobraCmd
}

func (cmd *hardwareCommand) run(_ *cobra.Command, _ []string) error {
	if cmd.format != "json" && cmd.format != "plain" {
		return fmt.Errorf("unknown format %q", cmd.format)
	}

	info, err := cmd.fetchHardwareInfoWithSpinner()
	if err != nil {
		return err
	}

	return cmd.printHardwareInfo(*cmd.newHardwareDetails(info))
}

func (info hardwareDetailsVerbose) compactHardwareDetails() hardwareDetails {
	h := hardwareDetails{
		Memory: string(fmt.Sprintf("%v (Swap %v)", common.FormatBytes(info.Memory.TotalRam), common.FormatBytes(info.Memory.TotalSwap))),
	}

	for _, c := range info.CPUs {
		name := c.Architecture
		if c.Architecture == cpu.Amd64 {
			name = strings.TrimSpace(fmt.Sprintf("%s (%s)", c.Architecture, c.ManufacturerId))
		}
		h.CPUs = append(h.CPUs, name)
	}

	for _, a := range info.Accelerators {
		switch d := a.(type) {
		case pciDeviceDetailsVerbose:
			h.Accelerators = append(h.Accelerators, d.compactName())
		case usbDeviceDetailsVerbose:
			h.Accelerators = append(h.Accelerators, d.compactName())
		case fastRPCDeviceDetailsVerbose:
			h.Accelerators = append(h.Accelerators, d.Bus)
		case apuSysDeviceDetailsVerbose:
			h.Accelerators = append(h.Accelerators, d.VendorName)
		}
	}

	for _, d := range info.Disk {
		path := d.Path
		if d.MountPoint != nil {
			path = *d.MountPoint
		}
		h.Disk = append(h.Disk, string(fmt.Sprintf("%s (Free %s / %s)", path, common.FormatBytes(d.Avail), common.FormatBytes(d.Total))))
	}

	return h
}

func (cmd *hardwareCommand) printHardwareInfo(info hardwareDetailsVerbose) error {
	switch cmd.format {
	case "json":
		return cmd.printHardwareInfoJSON(info)
	case "plain":
		if !cmd.verbose {
			return cmd.printHardwareInfoPlain(info.compactHardwareDetails())
		}
		return cmd.printHardwareInfoPlain(info)
	default:
		return fmt.Errorf("unknown format %q", cmd.format)
	}
}

func (cmd *hardwareCommand) printHardwareInfoJSON(info hardwareDetailsVerbose) error {
	jsonString, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}
	fmt.Printf("%s\n", jsonString)
	return nil
}

func (cmd *hardwareCommand) printHardwareInfoPlain(info any) error {
	yamlString, err := yaml.Marshal(info)
	if err != nil {
		return fmt.Errorf("plain: %s", err)
	}
	fmt.Printf("%s", yamlString)
	return nil
}

func (cmd *hardwareCommand) fetchHardwareInfoWithSpinner() (*machine.Machine, error) {
	stopProgress := common.StartProgressSpinner("Gathering hardware information")
	hwInfo, warnings, err := machine.Get(host.Real(), true, false)
	stopProgress()

	if len(warnings) > 0 && cmd.verbose {
		for _, warning := range warnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("getting hardware info: %s", err)
	}
	hwInfo.CPUs = cmd.compactCPUs(hwInfo.CPUs)
	return hwInfo, nil
}

func (cmd *hardwareCommand) compactCPUs(cpus []cpu.CPU) []cpu.CPU {
	if len(cpus) == 0 {
		return cpus
	}

	compact := []cpu.CPU{cpus[0]}
	for i := 1; i < len(cpus); i++ {
		if cpus[i].Architecture != cpus[i-1].Architecture {
			compact = append(compact, cpus[i])
		}
	}
	return compact
}

func (cmd *hardwareCommand) newHardwareDetails(info *machine.Machine) *hardwareDetailsVerbose {
	if info == nil {
		return nil
	}

	v := &hardwareDetailsVerbose{
		Memory: memoryDetailsVerbose{
			TotalRam:  info.Memory.TotalRam,
			TotalSwap: info.Memory.TotalSwap,
		},
	}

	// Combine all devices into a single slice
	totalDevices := len(info.PCIDevices) + len(info.USBDevices) + len(info.FastRPCDevices) + len(info.APUSYSDevices)
	v.Accelerators = make([]any, 0, totalDevices)

	// Add PCI devices
	for _, d := range info.PCIDevices {
		pci := pciDeviceDetailsVerbose{
			Bus:           d.Bus,
			VendorName:    d.VendorName,
			DeviceName:    d.DeviceName,
			SubvendorName: d.SubvendorName,
			SubdeviceName: d.SubdeviceName,
		}
		pci.AdditionalProperties = pci.newPciAdditionalDeviceProperties(d.AdditionalProperties)
		v.Accelerators = append(v.Accelerators, pci)
	}

	// Add USB devices
	for _, d := range info.USBDevices {
		v.Accelerators = append(v.Accelerators, usbDeviceDetailsVerbose{
			Bus:                  d.Bus,
			VendorName:           d.VendorName,
			ProductName:          d.ProductName,
			AdditionalProperties: d.AdditionalProperties,
		})
	}

	// Add FastRPC devices
	for _, d := range info.FastRPCDevices {
		v.Accelerators = append(v.Accelerators, fastRPCDeviceDetailsVerbose{
			Bus:                  d.Bus,
			AdditionalProperties: d.AdditionalProperties,
		})
	}

	// Add APUSYS devices
	for _, d := range info.APUSYSDevices {
		v.Accelerators = append(v.Accelerators, apuSysDeviceDetailsVerbose{
			Bus:        d.Bus,
			VendorName: d.VendorName,
		})
	}

	if info.CPUs != nil {
		v.CPUs = make([]cpuDetailsVerbose, len(info.CPUs))
		for i, c := range info.CPUs {
			v.CPUs[i] = cpuDetailsVerbose{
				Architecture:   c.Architecture,
				ManufacturerId: c.ManufacturerId,
				ImplementerId:  hexInt(c.ImplementerId),
			}
		}
	}

	if info.Disk != nil {
		v.Disk = make([]diskDetailsVerbose, 0, len(info.Disk))
		for _, d := range info.Disk {
			v.Disk = append(v.Disk, diskDetailsVerbose{
				MountPoint: d.MountPoint,
				Path:       d.Path,
				Total:      d.Total,
				Avail:      d.Available,
			})
		}
	}

	return v
}

func (p pciDeviceDetailsVerbose) newPciAdditionalDeviceProperties(props map[string]string) *pciAdditionalDeviceProperties {
	if len(props) == 0 {
		return nil
	}

	ap := &pciAdditionalDeviceProperties{
		Microarchitecture: props["microarchitecture"],
		ComputeCapability: props["compute-capability"],
	}
	if v, ok := props["vram"]; ok {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			ap.Vram = n
		}
	}
	if *ap == (pciAdditionalDeviceProperties{}) {
		return nil
	}
	return ap
}
