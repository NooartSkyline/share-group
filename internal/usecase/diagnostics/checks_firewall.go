package diagnostics

import (
	"context"
	"fmt"
	"runtime"

	"lanshare/internal/domain"
)

const firewallSteps = "เปิด System Settings > Network > Firewall > Options… " +
	"แล้วตั้งให้โปรแกรม Share Group เป็น \"Allow incoming connections\" (กด + เพื่อเพิ่มถ้ายังไม่อยู่ในรายการ) จากนั้นกด \"ตรวจใหม่\""

// checkFirewall asks the macOS application firewall whether this program may
// accept incoming connections.
//
// Only wording this package has verified is understood. Anything else, such as
// what the tool prints for a program that is not in its list, is reported as
// unknown with reason parse_failed rather than guessed.
func (s *Service) checkFirewall(ctx context.Context, _ runEnv) domain.CheckResult {
	m := metaFirewall
	const source = "socketfilterfw"
	const manual = "ดูสถานะที่ System Settings > Network > Firewall ด้วยตนเอง"
	if runtime.GOOS != "darwin" {
		return m.unknown(ReasonCommandMissing,
			"ตรวจไฟร์วอลล์อัตโนมัติรองรับบน macOS เท่านั้น",
			"อนุญาตให้ Share Group รับการเชื่อมต่อขาเข้าใน Windows Defender Firewall (หรือไฟร์วอลล์ของระบบ) จากนั้นกด \"ตรวจใหม่\"")
	}

	out, err := s.runCommand(ctx, s.limits.Command, CommandSocketFilterFW, "--getglobalstate")
	if err != nil {
		return m.unknownFrom(err, source, manual)
	}
	global, err := ParseFirewallGlobal(out)
	if err != nil {
		return m.unknownFrom(err, source, manual)
	}
	if !global.Enabled {
		return m.ok("Firewall ปิดอยู่ จึงไม่ขวางการเชื่อมต่อจากอุปกรณ์อื่น")
	}
	if s.deps.ExePath == "" {
		return m.unknownFrom(fmt.Errorf("diagnostics: program path unknown"), source, manual)
	}

	// Firewall is on: block-all and the per-program rule both matter. Each
	// failure is kept so that a blocked verdict from one is still reported
	// when the other cannot be read.
	var blockAll bool
	out, blockErr := s.runCommand(ctx, s.limits.Command, CommandSocketFilterFW, "--getblockall")
	if blockErr == nil {
		blockAll, blockErr = ParseFirewallBlockAll(out)
	}
	var access AppAccess
	out, appErr := s.runCommand(ctx, s.limits.Command, CommandSocketFilterFW, "--getappblocked", s.deps.ExePath)
	if appErr == nil {
		access, appErr = ParseFirewallAppBlocked(out)
	}

	switch {
	case blockErr == nil && blockAll:
		return m.warn(
			"Firewall เปิดอยู่และตั้งเป็น \"Block all incoming connections\" อุปกรณ์อื่นจึงเข้ามาไม่ได้",
			"เปิด System Settings > Network > Firewall > Options… แล้วปิด \"Block all incoming connections\" จากนั้นกด \"ตรวจใหม่\"")
	case appErr == nil && access == AppBlocked:
		return m.warn(
			"Firewall เปิดอยู่ และ Share Group ถูกตั้งให้บล็อกการเชื่อมต่อขาเข้า อุปกรณ์อื่นจึงเข้ามาไม่ได้",
			firewallSteps)
	case blockErr == nil && appErr == nil && access == AppPermitted:
		return m.ok("Firewall เปิดอยู่ และอนุญาตให้ Share Group รับการเชื่อมต่อขาเข้า")
	}
	if blockErr != nil {
		return m.unknownFrom(blockErr, source, manual)
	}
	return m.unknownFrom(appErr, source, manual)
}
