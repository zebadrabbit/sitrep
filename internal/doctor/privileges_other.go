//go:build !linux

package doctor

func privileges() Section {
	return Section{"Privileges", []Check{{Fail, "platform", "capability checks need Linux"}}}
}
