//go:build windows

package probe

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"unsafe"
)

func prepareProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true} }
func constrainProcess(cmd *exec.Cmd) (func(), error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	closeJob := func() { _ = windows.CloseHandle(job) }
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
	info.ProcessMemoryLimit = 128 << 20
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); e != nil {
		closeJob()
		return nil, e
	}
	cpu := struct {
		ControlFlags uint32
		CpuRate      uint32
	}{ControlFlags: 1 | 4, CpuRate: 2000}
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu))); e != nil {
		closeJob()
		return nil, e
	}
	handle, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if e != nil {
		closeJob()
		return nil, e
	}
	defer windows.CloseHandle(handle)
	if e = windows.AssignProcessToJobObject(job, handle); e != nil {
		closeJob()
		return nil, e
	}
	return closeJob, nil
}
