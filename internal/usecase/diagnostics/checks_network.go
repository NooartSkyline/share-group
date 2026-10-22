package diagnostics

import (
	"context"
	"fmt"
	"time"

	"lanshare/internal/domain"
)

// runCommand runs one external command with its own timeout.
func (s *Service) runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	if s.deps.Runner == nil {
		return nil, fmt.Errorf("diagnostics: no command runner")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := s.deps.Runner.Run(cctx, name, args...)
	if err != nil {
		// A command killed by its own timeout reports as a timeout even if the
		// runner wrapped the error.
		if cctx.Err() != nil && ctx.Err() == nil {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("diagnostics: run command: %w", err)
	}
	return out, nil
}

// activeInterface returns the interface the join URL points at.
func (s *Service) activeInterface(ctx context.Context) (domain.NetInterface, error) {
	if s.deps.Network == nil {
		return domain.NetInterface{}, fmt.Errorf("diagnostics: no network service")
	}
	n, err := s.deps.Network.Active(ctx)
	if err != nil {
		return domain.NetInterface{}, fmt.Errorf("diagnostics: active interface: %w", err)
	}
	return n, nil
}

// checkInterface warns when the advertised interface is a VPN or virtual
// adapter, or when several physical interfaces have an IPv4 address, because
// other devices may then be pointed at an address they cannot reach.
func (s *Service) checkInterface(ctx context.Context, _ runEnv) domain.CheckResult {
	m := metaInterface
	active, err := s.activeInterface(ctx)
	if err != nil {
		return m.unknownFrom(err, "รายการการ์ดเครือข่าย", "ตรวจว่า Mac เชื่อมต่อ Wi-Fi หรือสายแลนอยู่")
	}
	if s.deps.Interfaces == nil {
		return m.unknownFrom(fmt.Errorf("diagnostics: no interface provider"), "รายการการ์ดเครือข่าย", "")
	}
	all, err := s.deps.Interfaces.Interfaces(ctx)
	if err != nil {
		return m.unknownFrom(err, "รายการการ์ดเครือข่าย", "ตรวจว่า Mac เชื่อมต่อ Wi-Fi หรือสายแลนอยู่")
	}

	physical := map[string]struct{}{}
	for _, n := range all {
		if n.Kind == domain.NetPhysical {
			physical[n.Name] = struct{}{}
		}
	}
	where := fmt.Sprintf("%s (%s)", active.Name, active.IP)

	switch {
	case active.Kind == domain.NetUSB:
		return m.warn(
			fmt.Sprintf("ที่อยู่ที่ใช้เชิญอุปกรณ์คือสาย USB %s", where),
			"อุปกรณ์บน Wi-Fi จะเปิดลิงก์นี้ไม่ได้ ให้เลือกการ์ด Wi-Fi หรือสายแลนสำหรับ QR หลัก แล้วใช้แผงสายเคเบิลกับมือถือที่เสียบสาย")
	case active.Kind == domain.NetVPN || active.Kind == domain.NetVirtual:
		kind := "เสมือน"
		if active.Kind == domain.NetVPN {
			kind = "VPN"
		}
		return m.warn(
			fmt.Sprintf("ที่อยู่ที่ใช้เชิญอุปกรณ์คือ %s ซึ่งเป็นการ์ดเครือข่าย%s", where, kind),
			"อุปกรณ์อื่นอาจเข้าถึงที่อยู่นี้ไม่ได้ ให้เลือกการ์ดเครือข่ายของ Wi-Fi หรือสายแลนในหน้าเลือกเครือข่ายของ Owner")
	case len(physical) > 1:
		return m.warn(
			fmt.Sprintf("ที่อยู่ที่ใช้เชิญอุปกรณ์คือ %s และ Mac มีการ์ดเครือข่ายจริงที่มี IPv4 อยู่ %d ตัว", where, len(physical)),
			"เลือกการ์ดเครือข่ายที่อยู่วงเดียวกับอุปกรณ์อื่น (เช่น Wi-Fi เดียวกัน) ในหน้าเลือกเครือข่ายของ Owner")
	}
	return m.ok("ใช้ " + where)
}

// checkReachability looks at which addresses reached the server recently.
// Seeing nobody is only indirect evidence, so the hint lists possible causes
// and never claims that AP Isolation is confirmed.
func (s *Service) checkReachability(ctx context.Context, _ runEnv) domain.CheckResult {
	m := metaReachability
	if s.deps.Peers == nil || s.deps.Locals == nil {
		return m.unknownFrom(fmt.Errorf("diagnostics: peer log unavailable"), "บันทึกคำขอ", "")
	}
	active, err := s.activeInterface(ctx)
	if err != nil {
		return m.unknownFrom(err, "รายการการ์ดเครือข่าย", "ตรวจว่า Mac เชื่อมต่อ Wi-Fi หรือสายแลนอยู่")
	}
	locals, err := s.deps.Locals.LocalAddrs(ctx)
	if err != nil {
		return m.unknownFrom(err, "รายการที่อยู่ของเครื่องนี้", "")
	}

	peers := s.deps.Peers.Since(s.deps.Clock.Now().Add(-reachWindow))
	r := ClassifyReach(peers, locals, active)
	switch r.Verdict {
	case ReachOK:
		return m.ok(fmt.Sprintf("คำขอล่าสุดจากอุปกรณ์อื่นเมื่อ %s จากที่อยู่ %s",
			r.Latest.LastSeen.Format("2006-01-02 15:04:05"), r.Latest.Addr))
	case ReachOutOfSubnet:
		return m.warn(
			fmt.Sprintf("พบคำขอจากที่อยู่ %s ซึ่งอยู่นอกวงเครือข่ายของ %s (%s)",
				r.OutOfSubnet[0].Addr, active.Name, active.IP),
			"อุปกรณ์นั้นอาจอยู่บน Wi-Fi แขก (Guest) หรือเครือข่ายย่อย (VLAN) อื่น ให้ต่อ Wi-Fi เครือข่ายเดียวกับ Mac")
	}
	return m.warn(
		"ยังไม่พบคำขอจากอุปกรณ์อื่นใน 10 นาทีที่ผ่านมา",
		"ถ้าอุปกรณ์อื่นเปิดหน้าเว็บไม่ได้ ให้ลอง: 1) ตรวจว่าเราเตอร์เปิด AP Isolation (Client Isolation) หรือไม่ "+
			"2) ตรวจ Firewall ของ Mac 3) ให้ทุกอุปกรณ์ใช้ Wi-Fi เครือข่ายเดียวกัน "+
			"(ผลนี้เป็นเพียงข้อสังเกตทางอ้อม ยังไม่ยืนยันว่าเป็นสาเหตุใด)")
}

// checkWifiBand reads the band of the Wi-Fi channel of the active interface.
func (s *Service) checkWifiBand(ctx context.Context, _ runEnv) domain.CheckResult {
	m := metaWifiBand
	active, err := s.activeInterface(ctx)
	if err != nil {
		return m.unknownFrom(err, "รายการการ์ดเครือข่าย", "ตรวจว่า Mac เชื่อมต่อ Wi-Fi อยู่")
	}

	out, err := s.runCommand(ctx, s.limits.Command, CommandNetworkSetup, "-listallhardwareports")
	if err != nil {
		return m.unknownFrom(err, "networksetup", "")
	}
	ports, err := ParseHardwarePorts(out)
	if err != nil {
		return m.unknownFrom(err, "networksetup", "")
	}
	if !IsWiFiDevice(ports, active.Name) {
		return m.unknown(ReasonNotWifi,
			fmt.Sprintf("การ์ดเครือข่ายที่ใช้เชิญอุปกรณ์ (%s) ไม่ใช่ Wi-Fi จึงไม่มีย่านความถี่ให้ตรวจ", active.Name),
			"")
	}

	out, err = s.runCommand(ctx, s.limits.Slow, CommandSystemProfiler, "SPAirPortDataType", "-json")
	if err != nil {
		return m.unknownFrom(err, "system_profiler", "")
	}
	ch, err := ParseWifiChannelFor(out, active.Name)
	if err != nil {
		return m.unknownFrom(err, "system_profiler", "ตรวจว่า Mac เชื่อมต่อ Wi-Fi อยู่")
	}

	detail := fmt.Sprintf("ย่านความถี่ %s (ช่อง %d กว้าง %d MHz)", bandLabel(ch.Band), ch.Number, ch.WidthMHz)
	if ch.Band == Band2GHz {
		return m.warn(detail,
			"ย่าน 2.4 GHz ช้ากว่าและติดขัดง่ายกว่า แนะนำให้ย้ายทุกอุปกรณ์ไปใช้ Wi-Fi ย่าน 5 GHz")
	}
	return m.ok(detail)
}

// bandLabel is the user-facing name of a band (macOS prints 2.4 GHz as "2GHz").
func bandLabel(b WifiBand) string {
	switch b {
	case Band2GHz:
		return "2.4 GHz"
	case Band5GHz:
		return "5 GHz"
	case Band6GHz:
		return "6 GHz"
	}
	return string(b)
}
