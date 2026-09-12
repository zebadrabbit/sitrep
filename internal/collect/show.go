package collect

import "strings"

// ShowProps parses `systemctl show <units…> -p A,B` output, one blank-line
// separated block per unit, into Id → property → value.
func ShowProps(out []byte) map[string]map[string]string {
	units := map[string]map[string]string{}
	for _, block := range strings.Split(string(out), "\n\n") {
		props := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				props[k] = v
			}
		}
		if id := props["Id"]; id != "" {
			units[strings.TrimSuffix(id, ".service")] = props
		}
	}
	return units
}
