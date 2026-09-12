package system

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Sensor is one hwmon input: a temperature in °C (Max/Crit from the
// driver's thresholds, 0 when it has none) or a fan in RPM.
type Sensor struct {
	Chip  string  `json:"chip"`
	Label string  `json:"label,omitempty"`
	Value float64 `json:"value"`
	Max   float64 `json:"max,omitempty"`
	Crit  float64 `json:"crit,omitempty"`
}

// Fallback thresholds for drivers that publish none (acpitz, many
// embedded controllers). Silicon is unhappy past these on any box.
const tempWarnDefault, tempCritDefault = 80, 95

// Warn and Crit resolve a temperature's thresholds with the fallbacks.
func (s Sensor) Warn() float64 {
	if s.Max > 0 {
		return s.Max
	}
	return tempWarnDefault
}

func (s Sensor) CritAt() float64 {
	if s.Crit > 0 {
		return s.Crit
	}
	return tempCritDefault
}

// readSensors walks root (/sys/class/hwmon) for temp*_input and fan*_input.
// Chips come out in hwmonN order, inputs in numeric order; a missing tree
// yields nothing, never an error: sensors are optional.
func readSensors(root string) (temps, fans []Sensor) {
	chips, _ := filepath.Glob(filepath.Join(root, "hwmon*"))
	sort.Slice(chips, func(i, j int) bool { return hwmonNum(chips[i]) < hwmonNum(chips[j]) })
	for _, dir := range chips {
		name := strings.TrimSpace(readStr(filepath.Join(dir, "name")))
		if name == "" {
			continue
		}
		for _, in := range inputs(dir, "temp") {
			base := strings.TrimSuffix(in, "_input")
			v, ok := readMilli(in)
			if !ok {
				continue
			}
			s := Sensor{Chip: name, Label: strings.TrimSpace(readStr(base + "_label")), Value: v}
			s.Max, _ = readMilli(base + "_max")
			s.Crit, _ = readMilli(base + "_crit")
			temps = append(temps, s)
		}
		for _, in := range inputs(dir, "fan") {
			if rpm, err := strconv.ParseFloat(strings.TrimSpace(readStr(in)), 64); err == nil && rpm > 0 {
				fans = append(fans, Sensor{Chip: name, Label: strings.TrimSpace(readStr(strings.TrimSuffix(in, "_input") + "_label")), Value: rpm})
			}
		}
	}
	return temps, fans
}

func inputs(dir, kind string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, kind+"*_input"))
	sort.Slice(files, func(i, j int) bool { return inputNum(files[i], kind) < inputNum(files[j], kind) })
	return files
}

func hwmonNum(p string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(p), "hwmon"))
	return n
}

func inputNum(p, kind string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), kind), "_input"))
	return n
}

func readStr(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

// readMilli reads a millidegree file as degrees.
func readMilli(p string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(readStr(p)), 64)
	if err != nil {
		return 0, false
	}
	return v / 1000, true
}
