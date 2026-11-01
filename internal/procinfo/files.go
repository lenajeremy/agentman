package procinfo

// OpenFile is a file or directory a process holds open by descriptor.
type OpenFile struct {
	FD int
	// Path is where the file is, with any link on the way resolved, as lsof
	// names it.
	Path string
}
