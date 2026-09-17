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
	"gopkg.in/yaml.v3"
)

type HexInt uint64

func (h HexInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(fmt.Sprintf("0x%x", uint64(h)))
}

func (h HexInt) MarshalYAML() (any, error) {
	return fmt.Sprintf("0x%x", uint64(h)), nil
}

type hardwareCommand struct {
	*common.Context

	// flags
	verbose bool
	format  string
}

type machineDetails struct {
	CPUs         []cpuDetails  `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	Memory       memoryDetails `json:"memory,omitempty" yaml:"memory,omitempty"`
	Disk         []diskDetails `json:"disks,omitempty" yaml:"disks,omitempty"`
	Accelerators []any         `json:"accelerators,omitempty" yaml:"accelerators,omitempty"`
	verbose      bool
}

func (md machineDetails) MarshalYAML() (any, error) {
	if md.verbose {
		return struct {
			CPUs         []cpuDetails  `yaml:"cpus,omitempty"`
			Memory       memoryDetails `yaml:"memory,omitempty"`
			Disk         []diskDetails `yaml:"disks,omitempty"`
			Accelerators []any         `yaml:"accelerators,omitempty"`
		}{
			CPUs:         md.CPUs,
			Memory:       md.Memory,
			Disk:         md.Disk,
			Accelerators: md.Accelerators,
		}, nil
	} else {
		return struct {
			CPUs         []cpuDetails  `yaml:"cpus,omitempty"`
			Accelerators []any         `yaml:"accelerators,omitempty"`
			Memory       memoryDetails `yaml:"memory,omitempty"`
			Disk         []diskDetails `yaml:"disks,omitempty"`
		}{
			CPUs:         md.CPUs,
			Memory:       md.Memory,
			Disk:         md.Disk,
			Accelerators: md.Accelerators,
		}, nil
	}
}

type cpuDetails struct {
	Architecture   string `json:"architecture" yaml:"architecture"`
	ManufacturerId string `json:"manufacturer-id,omitempty" yaml:"manufacturer-id,omitempty"`
	ImplementerId  HexInt `json:"implementer-id,omitempty" yaml:"implementer-id,omitempty"`
	Verbose        bool
}

func (c cpuDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Architecture   string `json:"architecture,omitempty"`
		ManufacturerId string `json:"manufacturer-id,omitempty"`
		ImplementerId  HexInt `json:"implementer-id,omitempty"`
	}{
		Architecture:   c.Architecture,
		ManufacturerId: c.ManufacturerId,
		ImplementerId:  c.ImplementerId,
	})
}

func (c cpuDetails) MarshalYAML() (any, error) {
	if c.Verbose {
		return struct {
			Architecture   string `yaml:"architecture,omitempty"`
			ManufacturerId string `yaml:"manufacturer-id,omitempty"`
			ImplementerId  HexInt `yaml:"implementer-id,omitempty"`
		}{
			Architecture:   c.Architecture,
			ManufacturerId: c.ManufacturerId,
			ImplementerId:  c.ImplementerId,
		}, nil
	}
	switch c.Architecture {
	case cpu.Amd64:
		if c.ManufacturerId == "" {
			return c.Architecture, nil
		}
		return fmt.Sprintf("%s (%s)", c.Architecture, c.ManufacturerId), nil
	case cpu.Arm64, cpu.Riscv64:
		return c.Architecture, nil
	default:
		return nil, fmt.Errorf("unsupported architecture: %s", c.Architecture)
	}
}

type memoryDetails struct {
	TotalRam  uint64 `json:"total-ram" yaml:"total-ram"`
	TotalSwap uint64 `json:"total-swap" yaml:"total-swap"`
	Verbose   bool
}

func (m memoryDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TotalRam  any `json:"total-ram"`
		TotalSwap any `json:"total-swap"`
	}{
		TotalRam:  m.TotalRam,
		TotalSwap: m.TotalSwap,
	})
}

func (m memoryDetails) MarshalYAML() (any, error) {
	if m.Verbose {
		return struct {
			TotalRam  any `yaml:"total-ram"`
			TotalSwap any `yaml:"total-swap"`
		}{
			TotalRam:  m.TotalRam,
			TotalSwap: m.TotalSwap,
		}, nil
	}
	return fmt.Sprintf("%d (Swap %d)", m.TotalRam, m.TotalSwap), nil
}

type diskDetails struct {
	MountPoint *string `json:"mount-point,omitempty" yaml:"mount-point,omitempty"`
	Path       string  `json:"path" yaml:"path"`
	Total      uint64  `json:"total" yaml:"total"`
	Avail      uint64  `json:"avail" yaml:"avail"`
	Verbose    bool
}

func (d diskDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		MountPoint *string `json:"mount-point,omitempty"`
		Path       string  `json:"path"`
		Total      any     `json:"total"`
		Avail      any     `json:"avail"`
	}{
		MountPoint: d.MountPoint,
		Path:       d.Path,
		Total:      d.Total,
		Avail:      d.Avail,
	})
}

func (d diskDetails) MarshalYAML() (any, error) {
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
	}
	var diskPath string
	if d.MountPoint != nil {
		diskPath = *d.MountPoint
	} else {
		diskPath = d.Path
	}
	return fmt.Sprintf("%s (Free %s / %s)", diskPath, FormatBytes(d.Avail), FormatBytes(d.Total)), nil
}

type pciDeviceDetails struct {
	Bus                  string                         `json:"bus" yaml:"bus"`
	VendorName           string                         `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	DeviceName           string                         `json:"device-name,omitempty" yaml:"device-name,omitempty"`
	SubvendorName        string                         `json:"subvendor-name,omitempty" yaml:"subvendor-name,omitempty"`
	SubdeviceName        string                         `json:"subdevice-name,omitempty" yaml:"subdevice-name,omitempty"`
	AdditionalProperties *pciAdditionalDeviceProperties `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
	Verbose              bool                           `json:"-" yaml:"-"`
}

func (p pciDeviceDetails) MarshalYAML() (any, error) {
	if p.Verbose {
		return struct {
			Bus                  string                         `yaml:"bus"`
			VendorName           string                         `yaml:"vendor-name,omitempty"`
			DeviceName           string                         `yaml:"device-name,omitempty"`
			SubvendorName        string                         `yaml:"subvendor-name,omitempty"`
			SubdeviceName        string                         `yaml:"subdevice-name,omitempty"`
			AdditionalProperties *pciAdditionalDeviceProperties `yaml:"additional-properties,omitempty"`
		}{
			Bus:                  p.Bus,
			VendorName:           p.VendorName,
			DeviceName:           p.DeviceName,
			SubvendorName:        p.SubvendorName,
			SubdeviceName:        p.SubdeviceName,
			AdditionalProperties: p.AdditionalProperties,
		}, nil
	}
	return p.compactName(), nil
}

func (p pciDeviceDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Bus                  string                         `json:"bus"`
		VendorName           string                         `json:"vendor-name,omitempty"`
		DeviceName           string                         `json:"device-name,omitempty"`
		SubvendorName        string                         `json:"subvendor-name,omitempty"`
		SubdeviceName        string                         `json:"subdevice-name,omitempty"`
		AdditionalProperties *pciAdditionalDeviceProperties `json:"additional-properties,omitempty"`
	}{
		Bus:                  p.Bus,
		VendorName:           p.VendorName,
		DeviceName:           p.DeviceName,
		SubvendorName:        p.SubvendorName,
		SubdeviceName:        p.SubdeviceName,
		AdditionalProperties: p.AdditionalProperties,
	})
}

func (p pciDeviceDetails) compactName() string {
	name := strings.TrimSpace(fmt.Sprintf("%s %s", p.VendorName, p.DeviceName))
	if p.AdditionalProperties == nil {
		return name
	}
	return fmt.Sprintf("%s (VRAM %v)", name, FormatBytes(p.AdditionalProperties.Vram))
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
		Vram:              FormatBytes(a.Vram),
		ComputeCapability: a.ComputeCapability,
	}, nil
}

func (a pciAdditionalDeviceProperties) MarshalJSON() ([]byte, error) {
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

type usbDeviceDetails struct {
	Bus                  string            `json:"bus" yaml:"bus"`
	VendorName           string            `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	ProductName          string            `json:"product-name,omitempty" yaml:"product-name,omitempty"`
	AdditionalProperties map[string]string `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
	Verbose              bool              `json:"-" yaml:"-"`
}

func (u usbDeviceDetails) MarshalYAML() (any, error) {
	return struct {
		Bus                  string            `yaml:"bus"`
		VendorName           string            `yaml:"vendor-name,omitempty"`
		ProductName          string            `yaml:"product-name,omitempty"`
		AdditionalProperties map[string]string `yaml:"additional-properties,omitempty"`
	}{
		Bus:                  u.Bus,
		VendorName:           u.VendorName,
		ProductName:          u.ProductName,
		AdditionalProperties: u.AdditionalProperties,
	}, nil
}

func (u usbDeviceDetails) compactName() string {
	return strings.TrimSpace(fmt.Sprintf("%s %s", u.VendorName, u.ProductName))
}

func (p usbDeviceDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Bus                  string            `json:"bus"`
		VendorName           string            `json:"vendor-name,omitempty"`
		ProductName          string            `json:"product-name,omitempty"`
		AdditionalProperties map[string]string `json:"additional-properties,omitempty"`
	}{
		Bus:                  p.Bus,
		VendorName:           p.VendorName,
		ProductName:          p.ProductName,
		AdditionalProperties: p.AdditionalProperties,
	})
}

type fastRPCDeviceDetails struct {
	Bus                  string            `json:"bus" yaml:"bus"`
	AdditionalProperties map[string]string `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
	Verbose              bool              `json:"-" yaml:"-"`
}

func (f fastRPCDeviceDetails) MarshalYAML() (any, error) {
	if f.Verbose {
		return struct {
			Bus                  string            `yaml:"bus"`
			AdditionalProperties map[string]string `yaml:"additional-properties,omitempty"`
		}{
			Bus:                  f.Bus,
			AdditionalProperties: f.AdditionalProperties,
		}, nil
	}
	return f.Bus, nil
}

func (f fastRPCDeviceDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Bus                  string            `json:"bus"`
		AdditionalProperties map[string]string `json:"additional-properties,omitempty"`
	}{
		Bus:                  f.Bus,
		AdditionalProperties: f.AdditionalProperties,
	})
}

type apuSysDeviceDetails struct {
	Bus        string `json:"bus" yaml:"bus"`
	VendorName string `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	Verbose    bool   `json:"-" yaml:"-"`
}

func (a apuSysDeviceDetails) MarshalYAML() (any, error) {
	if a.Verbose {
		return struct {
			Bus        string `yaml:"bus"`
			VendorName string `yaml:"vendor-name,omitempty"`
		}{
			Bus:        a.Bus,
			VendorName: a.VendorName,
		}, nil
	}
	return a.VendorName, nil
}

func (a apuSysDeviceDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Bus        string `json:"bus"`
		VendorName string `json:"vendor-name,omitempty"`
	}{
		Bus:        a.Bus,
		VendorName: a.VendorName,
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

	info, err := cmd.fetchMachineInfoWithSpinner()
	if err != nil {
		return err
	}

	return cmd.printMachineInfo(*cmd.newMachineDetails(info))
}

func (cmd *hardwareCommand) printMachineInfo(info machineDetails) error {
	switch cmd.format {
	case "json":
		return cmd.printMachineInfoJson(info)
	case "plain":
		return cmd.printMachineInfoPlain(info)
	default:
		return fmt.Errorf("unknown format %q", cmd.format)
	}
}

func (cmd *hardwareCommand) printMachineInfoJson(info machineDetails) error {
	jsonString, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}
	fmt.Printf("%s\n", jsonString)
	return nil
}

func (cmd *hardwareCommand) printMachineInfoPlain(info machineDetails) error {
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
	hwInfo.CPUs = compactCpus(hwInfo.CPUs)
	return hwInfo, nil
}

func compactCpus(cpus []cpu.CPU) []cpu.CPU {
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

func (cmd *hardwareCommand) newMachineDetails(info *machine.Machine) *machineDetails {
	if info == nil {
		return nil
	}

	v := &machineDetails{
		verbose: cmd.verbose,
		Memory: memoryDetails{
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
		v.Accelerators = append(v.Accelerators, pciDeviceDetails{
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
		v.Accelerators = append(v.Accelerators, usbDeviceDetails{
			Bus:                  d.Bus,
			VendorName:           d.VendorName,
			ProductName:          d.ProductName,
			AdditionalProperties: d.AdditionalProperties,
			Verbose:              cmd.verbose,
		})
	}

	// Add FastRPC devices
	for _, d := range info.FastRPCDevices {
		v.Accelerators = append(v.Accelerators, fastRPCDeviceDetails{
			Bus:                  d.Bus,
			AdditionalProperties: d.AdditionalProperties,
			Verbose:              cmd.verbose,
		})
	}

	// Add APUSYS devices
	for _, d := range info.APUSYSDevices {
		v.Accelerators = append(v.Accelerators, apuSysDeviceDetails{
			Bus:        d.Bus,
			VendorName: d.VendorName,
			Verbose:    cmd.verbose,
		})
	}

	if info.CPUs != nil {
		v.CPUs = make([]cpuDetails, len(info.CPUs))
		for i, c := range info.CPUs {
			v.CPUs[i] = cpuDetails{
				Architecture:   c.Architecture,
				ManufacturerId: c.ManufacturerId,
				ImplementerId:  HexInt(c.ImplementerId),
				Verbose:        cmd.verbose,
			}
		}
	}

	if info.Disk != nil {
		v.Disk = make([]diskDetails, 0, len(info.Disk))
		for _, d := range info.Disk {
			v.Disk = append(v.Disk, diskDetails{
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

func newPciAdditionalDeviceProperties(props map[string]string) *pciAdditionalDeviceProperties {
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
