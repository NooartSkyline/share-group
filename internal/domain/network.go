package domain

// NetKind classifies a network interface so the right one can be advertised.
type NetKind string

const (
	NetPhysical NetKind = "physical" // Wi-Fi or Ethernet
	NetVPN      NetKind = "vpn"
	NetVirtual  NetKind = "virtual" // bridges, docker, vm adapters
)

// NetInterface is a usable IPv4 address on this machine.
type NetInterface struct {
	Name string
	IP   string
	Kind NetKind
}
