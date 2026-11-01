//go:build !darwin

package procinfo

// Files lists the files and directories a process holds open by descriptor.
func Files(int) ([]OpenFile, error) { return nil, ErrUnsupported }
