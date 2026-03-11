package fs

type HostOptions struct {
	Root             string
	VirtualRoot      string
	MaxFileReadBytes int64
}

type HostFactory struct {
	Root             string
	VirtualRoot      string
	MaxFileReadBytes int64
}

const (
	defaultHostVirtualRoot      = "/home/agent/project"
	defaultHostMaxFileReadBytes = 10 << 20
)

func (f HostFactory) options() HostOptions {
	return HostOptions(f)
}
