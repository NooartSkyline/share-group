package network

import (
	"net"
	"strings"

	"lanshare/internal/domain"
)

var (
	appleUSBNet   = mustCIDR("172.20.10.0/28")  // iPhone/iPad Personal Hotspot
	androidUSBNet = mustCIDR("192.168.42.0/24") // Android USB tethering
	winUSBNet     = mustCIDR("192.168.137.0/24")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// classifyUSB marks an interface as USB when the hardware port name or the
// IPv4 range is the one phones use for tethering. Kind that is already USB
// is left alone aside from filling in Label and a more specific device name.
func classifyUSB(n domain.NetInterface, hwPortName string) domain.NetInterface {
	device, label := usbIdentity(hwPortName, n.IP)
	if device == "" {
		if n.Kind == domain.NetUSB {
			if n.Label == "" {
				n.Label = n.Name
			}
		}
		return n
	}
	n.Kind = domain.NetUSB
	if n.Label == "" {
		n.Label = label
	}
	return n
}

// usbIdentity returns a cable device kind and a display label, or empty
// strings when the interface does not look like USB tethering.
func usbIdentity(hwPortName, ip string) (device, label string) {
	name := strings.TrimSpace(hwPortName)
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "iphone"):
		return domain.CableiPhone, orLabel(name, "iPhone USB")
	case strings.Contains(lower, "ipad"):
		return domain.CableiPad, orLabel(name, "iPad USB")
	case strings.Contains(lower, "rndis"), strings.Contains(lower, "ncm"), strings.Contains(lower, "android"):
		return domain.CableAndroid, orLabel(name, "Android USB")
	case strings.Contains(lower, "usb"):
		// "USB 10/100/1000 LAN" is an Ethernet adapter on a shared LAN unless
		// the address is a phone-tether range. Treat the shared-LAN case as
		// physical so it is advertised like Wi-Fi.
		if d, fallback := usbDeviceFromIP(ip); d != "" {
			return d, orLabel(name, fallback)
		}
		return "", ""
	}
	if d, fallback := usbDeviceFromIP(ip); d != "" {
		return d, fallback
	}
	return "", ""
}

// ViaUSB reports whether ip belongs to a phone USB tether subnet
// (iPhone Personal Hotspot or Android USB tethering).
func ViaUSB(ip string) bool {
	d, _ := usbDeviceFromIP(ip)
	return d != ""
}

func usbDeviceFromIP(ip string) (device, label string) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", ""
	}
	switch {
	case appleUSBNet.Contains(parsed):
		// 172.20.10.1 is the phone hosting Personal Hotspot, not a USB client.
		if v4 := parsed.To4(); v4 != nil && v4[3] == 1 {
			return "", ""
		}
		return domain.CableiPhone, "iPhone USB"
	case androidUSBNet.Contains(parsed):
		return domain.CableAndroid, "Android USB"
	case winUSBNet.Contains(parsed):
		return domain.CableAndroid, "Android USB"
	}
	return "", ""
}

func orLabel(name, fallback string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return fallback
}

func parseHardwarePortNames(out []byte) map[string]string {
	const (
		portKey   = "Hardware Port:"
		deviceKey = "Device:"
		vlanKey   = "VLAN Configurations"
	)
	m := map[string]string{}
	var name, device string
	flush := func() {
		if device != "" && name != "" {
			m[device] = name
		}
		name, device = "", ""
	}
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, vlanKey):
			flush()
			return m
		case strings.HasPrefix(line, portKey):
			flush()
			name = strings.TrimSpace(strings.TrimPrefix(line, portKey))
		case strings.HasPrefix(line, deviceKey):
			device = strings.TrimSpace(strings.TrimPrefix(line, deviceKey))
		}
	}
	flush()
	return m
}

func ioregQuoted(block, key string) string {
	needle := `"` + key + `" = "`
	i := strings.Index(block, needle)
	if i < 0 {
		return ""
	}
	rest := block[i+len(needle):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

var usbIgnoreProduct = []string{
	"hub", "billboard", "keyboard", "mouse", "trackpad", "headset",
	"headphone", "audio", "bluetooth", "camera", "webcam", "card reader",
	"cardreader", "usb 2.0", "usb 3.0", "usb3", "usb 3.1", "usb 3.2",
	"bus", "composite device", "hid",
}

var androidVendors = []string{
	"google", "samsung", "xiaomi", "oneplus", "huawei", "oppo", "vivo",
	"motorola", "sony", "nothing", "fairphone", "realme", "honor",
	"asus", "lenovo", "lge", "lg electronics", "htc", "zte", "nokia",
	"tcl", "nubia", "meizu", "infinix", "tecno",
}

func parseUSBPhones(out []byte) []CablePhone {
	chunks := strings.Split(string(out), "+-o ")
	var phones []CablePhone
	seen := map[string]struct{}{}
	for _, chunk := range chunks[1:] { // skip preamble
		product := ioregQuoted(chunk, "USB Product Name")
		if product == "" {
			product = ioregQuoted(chunk, "kUSBProductString")
		}
		vendor := ioregQuoted(chunk, "USB Vendor Name")
		if vendor == "" {
			vendor = ioregQuoted(chunk, "kUSBVendorString")
		}
		device, name := classifyUSBPhone(product, vendor)
		if device == "" {
			continue
		}
		key := device + "\x00" + name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		phones = append(phones, CablePhone{Device: device, Name: name})
	}
	return phones
}

func classifyUSBPhone(product, vendor string) (device, name string) {
	p := strings.TrimSpace(product)
	v := strings.TrimSpace(vendor)
	if p == "" {
		return "", ""
	}
	pl := strings.ToLower(p)
	vl := strings.ToLower(v)
	for _, bad := range usbIgnoreProduct {
		if pl == bad || strings.Contains(pl, bad) {
			// "USB 2.0 BILLBOARD" etc. "iPhone" never matches these as a whole.
			if !strings.Contains(pl, "iphone") && !strings.Contains(pl, "ipad") && !strings.Contains(pl, "android") {
				return "", ""
			}
		}
	}
	switch {
	case strings.Contains(pl, "iphone") || (strings.Contains(vl, "apple") && strings.Contains(pl, "iphone")):
		return domain.CableiPhone, p
	case strings.Contains(pl, "ipad") || (strings.Contains(vl, "apple") && strings.Contains(pl, "ipad")):
		return domain.CableiPad, p
	case strings.Contains(pl, "ipod"):
		return domain.CableiPhone, p
	case strings.Contains(vl, "apple"):
		return "", "" // other Apple USB (cables, displays, keyboards) already ignored or not a phone
	}
	for _, av := range androidVendors {
		if strings.Contains(vl, av) {
			return domain.CableAndroid, p
		}
	}
	if strings.Contains(pl, "adb") || strings.Contains(pl, "mtp") || strings.Contains(pl, "android") {
		return domain.CableAndroid, p
	}
	return "", ""
}

func cableDeviceOf(n domain.NetInterface, hwPortName string) string {
	d, _ := usbIdentity(hwPortName, n.IP)
	if d != "" {
		return d
	}
	if n.Kind == domain.NetUSB {
		return domain.CableGeneric
	}
	return ""
}
