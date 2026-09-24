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

type hardwareDetails struct {
	CPUs         []cpuDetails  `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	Accelerators []any         `json:"accelerators,omitempty" yaml:"accelerators,omitempty"`
	Memory       memoryDetails `json:"memory,omitempty" yaml:"memory,omitempty"`
	Disk         []diskDetails `json:"disks,omitempty" yaml:"disks,omitempty"`
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
	ImplementerId  HexInt `json:"implementer-id,omitempty" yaml:"implementer-id,omitempty"`
}

type cpuDetails string

func (c cpuDetailsVerbose) MarshalJSON() ([]byte, error) {
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

func (c cpuDetailsVerbose) MarshalYAML() (any, error) {
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

type memoryDetailsVerbose struct {
	TotalRam  uint64 `json:"total-ram" yaml:"total-ram"`
	TotalSwap uint64 `json:"total-swap" yaml:"total-swap"`
}

type memoryDetails string

func (m memoryDetailsVerbose) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TotalRam  any `json:"total-ram"`
		TotalSwap any `json:"total-swap"`
	}{
		TotalRam:  m.TotalRam,
		TotalSwap: m.TotalSwap,
	})
}

func (m memoryDetailsVerbose) MarshalYAML() (any, error) {
	return struct {
		TotalRam  any `yaml:"total-ram"`
		TotalSwap any `yaml:"total-swap"`
	}{
		TotalRam:  FormatBytes(m.TotalRam),
		TotalSwap: FormatBytes(m.TotalSwap),
	}, nil
}

type diskDetailsVerbose struct {
	MountPoint *string `json:"mount-point,omitempty" yaml:"mount-point,omitempty"`
	Path       string  `json:"path" yaml:"path"`
	Total      uint64  `json:"total" yaml:"total"`
	Avail      uint64  `json:"avail" yaml:"avail"`
}

type diskDetails string

func (d diskDetailsVerbose) MarshalJSON() ([]byte, error) {
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

func (d diskDetailsVerbose) MarshalYAML() (any, error) {
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

type pciDeviceDetailsVerbose struct {
	Bus                  string                         `json:"bus" yaml:"bus"`
	VendorName           string                         `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	DeviceName           string                         `json:"device-name,omitempty" yaml:"device-name,omitempty"`
	SubvendorName        string                         `json:"subvendor-name,omitempty" yaml:"subvendor-name,omitempty"`
	SubdeviceName        string                         `json:"subdevice-name,omitempty" yaml:"subdevice-name,omitempty"`
	AdditionalProperties *pciAdditionalDeviceProperties `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
}

type pciDeviceDetails string

func (p pciDeviceDetailsVerbose) MarshalYAML() (any, error) {
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

func (p pciDeviceDetailsVerbose) MarshalJSON() ([]byte, error) {
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

func (p pciDeviceDetailsVerbose) compactName() string {
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

type usbDeviceDetailsVerbose struct {
	Bus                  string            `json:"bus" yaml:"bus"`
	VendorName           string            `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
	ProductName          string            `json:"product-name,omitempty" yaml:"product-name,omitempty"`
	AdditionalProperties map[string]string `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
}

type usbDeviceDetails string

func (u usbDeviceDetailsVerbose) MarshalYAML() (any, error) {
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

func (u usbDeviceDetailsVerbose) compactName() string {
	return strings.TrimSpace(fmt.Sprintf("%s %s", u.VendorName, u.ProductName))
}

func (p usbDeviceDetailsVerbose) MarshalJSON() ([]byte, error) {
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

type fastRPCDeviceDetailsVerbose struct {
	Bus                  string            `json:"bus" yaml:"bus"`
	AdditionalProperties map[string]string `json:"additional-properties,omitempty" yaml:"additional-properties,omitempty"`
}

type fastRPCDeviceDetails string

func (f fastRPCDeviceDetailsVerbose) MarshalYAML() (any, error) {
	return struct {
		Bus                  string            `yaml:"bus"`
		AdditionalProperties map[string]string `yaml:"additional-properties,omitempty"`
	}{
		Bus:                  f.Bus,
		AdditionalProperties: f.AdditionalProperties,
	}, nil
}

func (f fastRPCDeviceDetailsVerbose) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Bus                  string            `json:"bus"`
		AdditionalProperties map[string]string `json:"additional-properties,omitempty"`
	}{
		Bus:                  f.Bus,
		AdditionalProperties: f.AdditionalProperties,
	})
}

type apuSysDeviceDetailsVerbose struct {
	Bus        string `json:"bus" yaml:"bus"`
	VendorName string `json:"vendor-name,omitempty" yaml:"vendor-name,omitempty"`
}

type apuSysDeviceDetails string

func (a apuSysDeviceDetailsVerbose) MarshalYAML() (any, error) {
	return struct {
		Bus        string `yaml:"bus"`
		VendorName string `yaml:"vendor-name,omitempty"`
	}{
		Bus:        a.Bus,
		VendorName: a.VendorName,
	}, nil
}

func (a apuSysDeviceDetailsVerbose) MarshalJSON() ([]byte, error) {
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

func compactHardwareDetails(info hardwareDetailsVerbose) hardwareDetails {
	h := hardwareDetails{
		Memory: memoryDetails(fmt.Sprintf("%v (Swap %v)", FormatBytes(info.Memory.TotalRam), FormatBytes(info.Memory.TotalSwap))),
	}

	for _, c := range info.CPUs {
		name := c.Architecture
		if c.Architecture == cpu.Amd64 {
			name = strings.TrimSpace(fmt.Sprintf("%s (%s)", c.Architecture, c.ManufacturerId))
		}
		h.CPUs = append(h.CPUs, cpuDetails(name))
	}

	for _, a := range info.Accelerators {
		switch d := a.(type) {
		case pciDeviceDetailsVerbose:
			h.Accelerators = append(h.Accelerators, pciDeviceDetails(d.compactName()))
		case usbDeviceDetailsVerbose:
			h.Accelerators = append(h.Accelerators, usbDeviceDetails(d.compactName()))
		case fastRPCDeviceDetailsVerbose:
			h.Accelerators = append(h.Accelerators, fastRPCDeviceDetails(d.Bus))
		case apuSysDeviceDetailsVerbose:
			h.Accelerators = append(h.Accelerators, apuSysDeviceDetails(d.VendorName))
		}
	}

	for _, d := range info.Disk {
		path := d.Path
		if d.MountPoint != nil {
			path = *d.MountPoint
		}
		h.Disk = append(h.Disk, diskDetails(fmt.Sprintf("%s (Free %s / %s)", path, FormatBytes(d.Avail), FormatBytes(d.Total))))
	}

	return h
}

func (cmd *hardwareCommand) printHardwareInfo(info hardwareDetailsVerbose) error {
	switch cmd.format {
	case "json":
		return cmd.printHardwareInfoJson(info)
	case "plain":
		if !cmd.verbose {
			return cmd.printHardwareInfoPlain(compactHardwareDetails(info))
		}
		return cmd.printHardwareInfoPlain(info)
	default:
		return fmt.Errorf("unknown format %q", cmd.format)
	}
}

func (cmd *hardwareCommand) printHardwareInfoJson(info hardwareDetailsVerbose) error {
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
		v.Accelerators = append(v.Accelerators, pciDeviceDetailsVerbose{
			Bus:                  d.Bus,
			VendorName:           d.VendorName,
			DeviceName:           d.DeviceName,
			SubvendorName:        d.SubvendorName,
			SubdeviceName:        d.SubdeviceName,
			AdditionalProperties: newPciAdditionalDeviceProperties(d.AdditionalProperties),
		})
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
				ImplementerId:  HexInt(c.ImplementerId),
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
