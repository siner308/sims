package proxy

// Process is the owner of a client connection on this machine.
type Process struct {
	PID  int
	Name string
	Path string
}
