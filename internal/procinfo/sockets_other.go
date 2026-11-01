//go:build !darwin

package procinfo

// Name returns the command name lsof reports for a process.
func Name(int) (string, error) { return "", ErrUnsupported }

// Cwd returns a process's working directory.
func Cwd(int) (string, error) { return "", ErrUnsupported }

// Listening finds the listening TCP sockets of every process that can be
// read, by pid.
func Listening() (map[int][]Listener, error) { return nil, ErrUnsupported }
