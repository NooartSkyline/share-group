package domain

// NetKind classifies a network interface so the right one can be advertised.
type NetKind string

const (
	NetPhysical NetKind = "physical" // Wi-Fi or Ethernet
	NetVPN      NetKind = "vpn"
	NetVirtual  NetKind = "virtual" // bridges, docker, vm adapters
	NetUSB      NetKind = "usb"     // USB tethering / iPhone USB / Android RNDIS
)

// Cable device kinds reported to the Owner when a phone is on USB.
const (
	CableiPhone  = "iphone"
	CableiPad    = "ipad"
	CableAndroid = "android"
	CableGeneric = "usb"
	CableHost    = "host" // files on this host (Android internal storage / OTG)
)

// NetInterface is a usable IPv4 address on this machine.
type NetInterface struct {
	Name string
	IP   string
	Kind NetKind
	// PrefixLen is the length of the subnet mask in bits (24 for /24), used to
	// tell whether a client address is on the same subnet. 0 means unknown.
	PrefixLen int
	// Label is an optional human name such as "iPhone USB". Empty means use Name.
	Label string
}
