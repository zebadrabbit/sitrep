//go:build !linux

package collect

import (
	"errors"
	"time"
)

const ClockTicks = 100

var errNotLinux = errors.New("proc: needs linux")

func (r *Runner) BootTime() (time.Time, error)                { return time.Time{}, errNotLinux }
func (r *Runner) ProcStart(int, time.Time) (time.Time, error) { return time.Time{}, errNotLinux }
func (r *Runner) ProcUID(int) (int, error)                    { return -1, errNotLinux }
func (r *Runner) ProcCmdline(int) string                      { return "" }
func (r *Runner) ProcCgroup(int) string                       { return "" }
func Username(int) string                                     { return "" }
