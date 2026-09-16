package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/lscompute/pkg/machine"
	"github.com/canonical/lscompute/pkg/machine/host"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type HexInt uint16

type hardwareCommand struct {
	*common.Context

	// flags
	verbose bool
	format  string
}

type MachineDetails struct {
	CPUs         []CpuDetails  `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	Memory       MemoryDetails `json:"memory,omitempty" yaml:"memory,omitempty"`
	Disk         []DiskDetails `json:"disks,omitempty" yaml:"disks,omitempty"`
	Accelerators []any         `json:"accelerators,omitempty" yaml:"accelerators,omitempty"`
	Verbose      bool          `json:"-" yaml:"-"`
}

func (md MachineDetails) MarshalYAML() (any, error) {
	if md.Verbose {
		return struct {
			CPUs         []CpuDetails  `yaml:"cpus,omitempty"`
			Memory       MemoryDetails `yaml:"memory,omitempty"`
			Disk         []DiskDetails `yaml:"disks,omitempty"`
			Accelerators []any         `yaml:"accelerators,omitempty"`
		}{
			CPUs:         md.CPUs,
			Memory:       md.Memory,
			Disk:         md.Disk,
			Accelerators: md.Accelerators,
		}, nil
	} else {
		return struct {
			CPUs         []CpuDetails  `yaml:"cpus,omitempty"`
			Accelerators []any         `yaml:"accelerators,omitempty"`
			Memory       MemoryDetails `yaml:"memory,omitempty"`
			Disk         []DiskDetails `yaml:"disks,omitempty"`
		}{
			CPUs:         md.CPUs,
			Memory:       md.Memory,
			Disk:         md.Disk,
			Accelerators: md.Accelerators,
		}, nil
	}
}

type CpuDetails struct {
	Architecture   string `json:"architecture" yaml:"architecture"`
	ManufacturerId string `json:"manufacturer-id,omitempty" yaml:"manufacturer-id,omitempty"`
	BrandString    string
	ModelName      *string
	Processor      int64
	Verbose        bool
}

func (cpu CpuDetails) MarshalJSON() ([]byte, error) {
	if cpu.Verbose {
		return json.Marshal(struct {
			Architecture   string `json:"architecture,omitempty"`
			ManufacturerId string `json:"manufacturer-id,omitempty"`
		}{
			Architecture:   cpu.Architecture,
			ManufacturerId: cpu.ManufacturerId,
		})
	}

	return json.Marshal(fmt.Sprintf("%s %d threads", cpu.name(), cpu.Processor))
}

func (cpu CpuDetails) MarshalYAML() (any, error) {
	if cpu.Verbose {
		return struct {
			Architecture   string `yaml:"architecture,omitempty"`
			ManufacturerId string `yaml:"manufacturer-id,omitempty"`
		}{
			Architecture:   cpu.Architecture,
			ManufacturerId: cpu.ManufacturerId,
		}, nil
	}
	return fmt.Sprintf("%s %d threads", cpu.name(), cpu.Processor), nil
}

func (cpu CpuDetails) name() string {
	if cpu.ModelName != nil && *cpu.ModelName != "" {
		return *cpu.ModelName
	}
	if cpu.BrandString != "" {
		return cpu.BrandString
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s", cpu.ManufacturerId, cpu.Architecture))
}

type MemoryDetails struct {
	TotalRam  uint64 `json:"total-ram" yaml:"total-ram"`
	TotalSwap uint64 `json:"total-swap" yaml:"total-swap"`
	Verbose   bool
}

func (m MemoryDetails) MarshalJSON() ([]byte, error) {
	if m.Verbose {
		return json.Marshal(struct {
			TotalRam  any `json:"total-ram"`
			TotalSwap any `json:"total-swap"`
		}{
			TotalRam:  FormatBytes(m.TotalRam),
			TotalSwap: FormatBytes(m.TotalSwap),
		})
	}
	return json.Marshal(fmt.Sprintf("%v (Swap %v)", FormatBytes(m.TotalRam), FormatBytes(m.TotalSwap)))
}

func (m MemoryDetails) MarshalYAML() (any, error) {

	if m.Verbose {
		return struct {
			TotalRam  any `yaml:"total-ram"`
			TotalSwap any `yaml:"total-swap"`
		}{
			TotalRam:  FormatBytes(m.TotalRam),
			TotalSwap: FormatBytes(m.TotalSwap),
		}, nil
	} else {
		return fmt.Sprintf("%v (Swap %v)", FormatBytes(m.TotalRam), FormatBytes(m.TotalSwap)), nil
	}
}

type DiskDetails struct {
	MountPoint *string `json:"mount-point,omitempty" yaml:"mount-point,omitempty"`
	Path       string  `json:"path" yaml:"path"`
	Total      uint64  `json:"total" yaml:"total"`
	Avail      uint64  `json:"avail" yaml:"avail"`
	Verbose    bool
}

func (d DiskDetails) MarshalJSON() ([]byte, error) {
	if d.Verbose {
		return json.Marshal(struct {
			MountPoint *string `json:"mount-point,omitempty"`
			Path       string  `json:"path"`
			Total      any     `json:"total"`
			Avail      any     `json:"avail"`
		}{
			MountPoint: d.MountPoint,
			Path:       d.Path,
			Total:      FormatBytes(d.Total),
			Avail:      FormatBytes(d.Avail),
		})
	}

	var diskPath string
	if d.MountPoint != nil {
		diskPath = *d.MountPoint
	} else {
		diskPath = d.Path
	}
	return json.Marshal(fmt.Sprintf("%s (Free %s / %s)", diskPath, FormatBytes(d.Avail), FormatBytes(d.Total)))
}

func (d DiskDetails) MarshalYAML() (any, error) {
	if d.Verbose {
		return struct {
			MountPoint *string `yaml:"mount-point,omitempty"`
			Path       string  `yaml:"path"`
			Total      any     `yaml:"total"`
			Avail      any     `yaml:"avail"`
		}{
			MountPoint: d.MountPoint,
			Path:       d.Path,
			Total:      FormatBytes(d.Total),
			Avail:      FormatBytes(d.Avail),
		}, nil
	} else {
		var diskPath string
		if d.MountPoint != nil {
			diskPath = *d.MountPoint
		} else {
			diskPath = d.Path
		}
		return fmt.Sprintf("%s (Free %s / %s)", diskPath, FormatBytes(d.Avail), FormatBytes(d.Total)), nil
	}
}

type PciDeviceDetails struct {
	Bus                  string                         `json:"bus" yaml:"bus"`
	VendorName           string                         `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	DeviceName           string                         `json:"device-name,omitempty" yaml:"device-name,omitempty"`
	SubvendorName        string                         `json:"subvendor-name,omitempty" yaml:"subvendor-name,omitempty"`
	SubdeviceName        string                         `json:"subdevice-name,omitempty" yaml:"subdevice-name,omitempty"`
	AdditionalProperties *PciAdditionalDeviceProperties `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
	Verbose              bool                           `json:"-" yaml:"-"`
}

func (p PciDeviceDetails) MarshalYAML() (any, error) {
	if p.Verbose {
		return struct {
			Bus                  string                         `yaml:"bus"`
			VendorName           string                         `yaml:"vendor-name,omitempty"`
			DeviceName           string                         `yaml:"device-name,omitempty"`
			SubvendorName        string                         `yaml:"subvendor-name,omitempty"`
			AdditionalProperties *PciAdditionalDeviceProperties `yaml:"additional-properties,omitempty"`
		}{
			Bus:                  p.Bus,
			VendorName:           p.VendorName,
			DeviceName:           p.DeviceName,
			SubvendorName:        p.SubvendorName,
			AdditionalProperties: p.AdditionalProperties,
		}, nil
	} else {
		return fmt.Sprintf("%s %s", p.VendorName, p.DeviceName), nil
	}
}

func (p PciDeviceDetails) MarshalJSON() ([]byte, error) {
	if p.Verbose {
		return json.Marshal(struct {
			Bus                  string                         `json:"bus"`
			VendorName           string                         `json:"vendor-name,omitempty"`
			DeviceName           string                         `json:"device-name,omitempty"`
			SubvendorName        string                         `json:"subvendor-name,omitempty"`
			AdditionalProperties *PciAdditionalDeviceProperties `json:"additional-properties,omitempty"`
		}{
			Bus:                  p.Bus,
			VendorName:           p.VendorName,
			DeviceName:           p.DeviceName,
			SubvendorName:        p.SubvendorName,
			AdditionalProperties: p.AdditionalProperties,
		})
	}
	return json.Marshal(fmt.Sprintf("%s %s", p.VendorName, p.DeviceName))
}

type UsbDeviceDetails struct {
	Bus                  string            `json:"bus" yaml:"bus"`
	VendorName           string            `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	ProductName          string            `json:"product-name,omitempty" yaml:"product-name,omitempty"`
	AdditionalProperties map[string]string `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
	Verbose              bool              `json:"-" yaml:"-"`
}

type FastRPCDeviceDetails struct {
	Bus                  string            `json:"bus" yaml:"bus"`
	AdditionalProperties map[string]string `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
	Verbose              bool              `json:"-" yaml:"-"`
}

type ApusysDeviceDetails struct {
	Bus        string `json:"bus" yaml:"bus"`
	VendorName string `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	Verbose    bool   `json:"-" yaml:"-"`
}

type PciAdditionalDeviceProperties struct {
	Microarchitecture string `json:"microarchitecture,omitempty" yaml:"microarchitecture,omitempty"`
	Vram              uint64 `json:"vram,omitempty" yaml:"vram,omitempty"`
	ComputeCapability string `json:"compute-capability,omitempty" yaml:"compute-capability,omitempty"`
}

func (a PciAdditionalDeviceProperties) MarshalYAML() (any, error) {
	return struct {
		Microarchitecture string `yaml:"microarchitecture,omitempty"`
		Vram              any    `yaml:"vram,omitempty"`
		ComputeCapability string `yaml:"compute-capability,omitempty"`
	}{
		Microarchitecture: a.Microarchitecture,
		Vram:              FormatBytes(a.Vram),
		ComputeCapability: a.ComputeCapability,
	}, nil
}

func (a PciAdditionalDeviceProperties) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Microarchitecture string `json:"microarchitecture,omitempty"`
		Vram              any    `json:"vram,omitempty"`
		ComputeCapability string `json:"compute-capability,omitempty"`
	}{
		Microarchitecture: a.Microarchitecture,
		Vram:              FormatBytes(a.Vram),
		ComputeCapability: a.ComputeCapability,
	})
}

func Hardware(ctx *common.Context) *cobra.Command {
	var cmd hardwareCommand
	cmd.Context = ctx

	cobraCmd := &cobra.Command{
		Use:               "hardware",
		Short:             "Print information about the host machine",
		Long:              "Print information about the host machine, including hardware and compute resources",
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
		fmt.Sprintf("output format (%s)", strings.Join(supportedFormats, ", ")),
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
	info, err := cmd.fetchMachineInfoWithSpinner()
	if err != nil {
		return err
	}

	return cmd.printMachineInfo(*cmd.newMachineDetails(info))
}

func (cmd *hardwareCommand) printMachineInfo(info MachineDetails) error {
	switch cmd.format {
	case "json":
		return cmd.printMachineInfoJson(info)
	case "plain":
		return cmd.printMachineInfoPlain(info)
	default:
		return fmt.Errorf("unknown format %q", cmd.format)
	}
}

func (cmd *hardwareCommand) printMachineInfoJson(info MachineDetails) error {
	jsonString, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}
	fmt.Printf("%s\n", jsonString)
	return nil
}

func (cmd *hardwareCommand) printMachineInfoPlain(info MachineDetails) error {
	yamlString, err := yaml.Marshal(info)
	if err != nil {
		return fmt.Errorf("plain: %s", err)
	}
	fmt.Printf("%s", yamlString)
	return nil
}

func (cmd *hardwareCommand) fetchMachineInfoWithSpinner() (*machine.Machine, error) {
	stopProgress := common.StartProgressSpinner("Gathering machine information")
	hwInfo, warnings, err := machine.Get(host.Real(), true, false)
	stopProgress()

	if len(warnings) > 0 && cmd.verbose {
		for _, warning := range warnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("getting machine info: %s", err)
	}

	return hwInfo, nil
}

func FormatBytes(b uint64) any {
	const (
		mib = 1024 * 1024
		gib = 1024 * mib
		tib = 1024 * gib
	)
	switch {
	case b >= tib:
		return fmt.Sprintf("%.1fT", float64(b)/tib)
	case b >= gib:
		return fmt.Sprintf("%.1fG", float64(b)/gib)
	case b >= mib:
		return fmt.Sprintf("%.1fM", float64(b)/mib)
	default:
		return b
	}
}

func (cmd *hardwareCommand) newMachineDetails(info *machine.Machine) *MachineDetails {
	if info == nil {
		return nil
	}

	v := &MachineDetails{
		Verbose: cmd.verbose,
		Memory: MemoryDetails{
			TotalRam:  info.Memory.TotalRam,
			TotalSwap: info.Memory.TotalSwap,
			Verbose:   cmd.verbose,
		},
	}

	// Combine all devices into a single slice
	totalDevices := len(info.PCIDevices) + len(info.USBDevices) + len(info.FastRPCDevices) + len(info.APUSYSDevices)
	v.Accelerators = make([]any, 0, totalDevices)

	// Add PCI devices
	for _, d := range info.PCIDevices {
		v.Accelerators = append(v.Accelerators, PciDeviceDetails{
			Bus:                  d.Bus,
			VendorName:           d.VendorName,
			DeviceName:           d.DeviceName,
			SubvendorName:        d.SubvendorName,
			SubdeviceName:        d.SubdeviceName,
			AdditionalProperties: newPciAdditionalDeviceProperties(d.AdditionalProperties),
			Verbose:              cmd.verbose,
		})
	}

	// Add USB devices
	for _, d := range info.USBDevices {
		v.Accelerators = append(v.Accelerators, UsbDeviceDetails{
			Bus:                  d.Bus,
			VendorName:           d.VendorName,
			ProductName:          d.ProductName,
			AdditionalProperties: d.AdditionalProperties,
			Verbose:              cmd.verbose,
		})
	}

	// Add FastRPC devices
	for _, d := range info.FastRPCDevices {
		v.Accelerators = append(v.Accelerators, FastRPCDeviceDetails{
			Bus:                  d.Bus,
			AdditionalProperties: d.AdditionalProperties,
			Verbose:              cmd.verbose,
		})
	}

	// Add APUSYS devices
	for _, d := range info.APUSYSDevices {
		v.Accelerators = append(v.Accelerators, ApusysDeviceDetails{
			Bus:        d.Bus,
			VendorName: d.VendorName,
		})
	}

	if info.CPUs != nil {
		v.CPUs = make([]CpuDetails, len(info.CPUs))
		for i, c := range info.CPUs {
			v.CPUs[i] = CpuDetails{
				Architecture:   c.Architecture,
				ManufacturerId: c.ManufacturerId,
				ModelName:      c.ModelName,
				Processor:      c.Processor,
				BrandString:    c.BrandString,
				Verbose:        cmd.verbose,
			}
		}
	}

	if info.Disk != nil {
		v.Disk = make([]DiskDetails, 0, len(info.Disk))
		for _, d := range info.Disk {
			v.Disk = append(v.Disk, DiskDetails{
				MountPoint: d.MountPoint,
				Path:       d.Path,
				Total:      d.Total,
				Avail:      d.Available,
				Verbose:    cmd.verbose,
			})
		}
	}

	return v
}

func newPciAdditionalDeviceProperties(props map[string]string) *PciAdditionalDeviceProperties {
	if len(props) == 0 {
		return nil
	}

	ap := &PciAdditionalDeviceProperties{
		Microarchitecture: props["microarchitecture"],
		ComputeCapability: props["compute-capability"],
	}
	if v, ok := props["vram"]; ok {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			ap.Vram = n
		}
	}
	if *ap == (PciAdditionalDeviceProperties{}) {
		return nil
	}
	return ap
}
