//go:build !darwin

package procinfo

// Supported reports whether this platform's process table is read here.
const Supported = false

// List reads the whole process table.
func List() ([]Process, error) { return nil, ErrUnsupported }

// Args returns the arguments a process was started with.
func Args(int) ([]string, error) { return nil, ErrUnsupported }

// Arg0 returns the name a process was run by.
func Arg0(int) (string, error) { return "", ErrUnsupported }

// Path returns the file a process is executing.
func Path(int) (string, error) { return "", ErrUnsupported }
