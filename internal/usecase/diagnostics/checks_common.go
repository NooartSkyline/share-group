package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"

	"lanshare/internal/domain"
)

// IDs of the seven checks, in the order they appear in a Report.
const (
	CheckInterface     = "interface"
	CheckFirewall      = "firewall"
	CheckReachability  = "reachability"
	CheckWifiBand      = "wifi_band"
	CheckDisk          = "disk"
	CheckPower         = "power"
	CheckSecureContext = "secure_context"
)

// External commands, always started from a fixed argv and never via a shell.
const (
	CommandSocketFilterFW = "/usr/libexec/ApplicationFirewall/socketfilterfw"
	CommandNetworkSetup   = "/usr/sbin/networksetup"
	CommandSystemProfiler = "/usr/sbin/system_profiler"
)

// runEnv carries the per-request input a check may need.
type runEnv struct {
	secure bool
}

// checkDef ties a check to its identity and a hint to show if it cannot run.
type checkDef struct {
	meta      meta
	retryHint string
	run       func(ctx context.Context, env runEnv) domain.CheckResult
}

// definitions lists the seven checks in report order.
func (s *Service) definitions() []checkDef {
	return []checkDef{
		{meta: metaInterface, retryHint: "ลองกด \"ตรวจใหม่\" อีกครั้ง", run: s.checkInterface},
		{meta: metaFirewall, retryHint: "ดูสถานะที่ System Settings > Network > Firewall ด้วยตนเอง", run: s.checkFirewall},
		{meta: metaReachability, retryHint: "ลองกด \"ตรวจใหม่\" อีกครั้ง", run: s.checkReachability},
		{meta: metaWifiBand, retryHint: "ดูย่านความถี่ได้จากไอคอน Wi-Fi บนแถบเมนูขณะกด Option ค้างไว้", run: s.checkWifiBand},
		{meta: metaDisk, retryHint: "ตรวจพื้นที่ว่างด้วย Finder หรือ Disk Utility", run: s.checkDisk},
		{meta: metaPower, retryHint: "ลองกด \"ตรวจใหม่\" อีกครั้ง", run: s.checkPower},
		{meta: metaSecureContext, retryHint: "", run: func(_ context.Context, env runEnv) domain.CheckResult {
			return secureContextResult(env.secure)
		}},
	}
}

// meta is the fixed identity of a check and the builder of its results.
type meta struct {
	id    string
	title string
}

var (
	metaInterface     = meta{CheckInterface, "การ์ดเครือข่ายที่ใช้เชิญอุปกรณ์"}
	metaFirewall      = meta{CheckFirewall, "Firewall ของ macOS"}
	metaReachability  = meta{CheckReachability, "อุปกรณ์อื่นเข้าถึงเซิร์ฟเวอร์ได้หรือไม่"}
	metaWifiBand      = meta{CheckWifiBand, "ย่านความถี่ Wi-Fi"}
	metaDisk          = meta{CheckDisk, "พื้นที่ดิสก์"}
	metaPower         = meta{CheckPower, "การกัน Mac หลับ"}
	metaSecureContext = meta{CheckSecureContext, "การเชื่อมต่อแบบปลอดภัย (HTTPS)"}
)

func (m meta) result(st domain.CheckStatus, detail, hint, reason string) domain.CheckResult {
	return domain.CheckResult{ID: m.id, Status: st, Title: m.title, Detail: detail, Hint: hint, Reason: reason}
}

func (m meta) ok(detail string) domain.CheckResult {
	return m.result(domain.CheckOK, detail, "", "")
}

func (m meta) warn(detail, hint string) domain.CheckResult {
	return m.result(domain.CheckWarn, detail, hint, "")
}

func (m meta) fail(detail, hint string) domain.CheckResult {
	return m.result(domain.CheckFail, detail, hint, "")
}

func (m meta) unknown(reason, detail, hint string) domain.CheckResult {
	logUnknown(m.id, reason)
	return m.result(domain.CheckUnknown, detail, hint, reason)
}

// unknownFrom reports the check as unknown because err stopped it. source is
// a fixed label such as "networksetup"; the error text itself is never shown.
func (m meta) unknownFrom(err error, source, hint string) domain.CheckResult {
	reason := reasonFor(err)
	return m.unknown(reason, reasonDetail(reason, source), hint)
}

// enforce guarantees the report invariants whatever a check returned: the
// identity is the check's own, a status is one of the four known values, a
// warn or fail always has a hint, and an unknown always has a reason.
func enforce(m meta, r domain.CheckResult) domain.CheckResult {
	r.ID, r.Title = m.id, m.title
	switch r.Status {
	case domain.CheckOK, domain.CheckWarn, domain.CheckFail, domain.CheckUnknown:
	default:
		r.Status = domain.CheckUnknown
		if r.Reason == "" {
			r.Reason = ReasonCommandFailed
		}
	}
	if (r.Status == domain.CheckWarn || r.Status == domain.CheckFail) && r.Hint == "" {
		r.Hint = "ตรวจรายละเอียดด้านบนแล้วแก้ตามข้อความ จากนั้นกด \"ตรวจใหม่\""
	}
	if r.Status == domain.CheckUnknown && r.Reason == "" {
		r.Reason = ReasonCommandFailed
	}
	return r
}

// reasonFor maps the failure of a command or a parse to a Reason code.
// Anything it does not recognise is a failed command.
func reasonFor(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return ReasonTimeout
	case errors.Is(err, fs.ErrNotExist):
		return ReasonCommandMissing
	case errors.Is(err, fs.ErrPermission):
		return ReasonPermissionDeny
	case errors.Is(err, ErrParseFailed):
		return ReasonParseFailed
	case errors.Is(err, ErrNoNetwork), errors.Is(err, domain.ErrNoNetwork):
		return ReasonNoNetwork
	}
	return ReasonCommandFailed
}

// reasonDetail is the Thai sentence for a reason code. source is a fixed
// label of where the data comes from.
func reasonDetail(reason, source string) string {
	switch reason {
	case ReasonCommandMissing:
		return "ไม่พบ " + source + " บนเครื่องนี้"
	case ReasonTimeout:
		return source + " ใช้เวลานานเกินกำหนดจึงถูกยกเลิก"
	case ReasonPermissionDeny:
		return "ไม่มีสิทธิ์เรียกใช้ " + source
	case ReasonParseFailed:
		return "อ่านผลจาก " + source + " ไม่เข้าใจ จึงยังบอกสถานะไม่ได้"
	case ReasonNoNetwork:
		return "ยังไม่ได้เชื่อมต่อเครือข่ายที่ใช้งานได้"
	}
	return source + " ทำงานไม่สำเร็จ"
}

// logUnknown records why a check could not run. Only the check ID and the
// reason code are logged, never an error text or any command output.
func logUnknown(id, reason string) {
	slog.Debug("diagnostics: check unknown", "check", id, "reason", reason)
}

// formatBytes renders a byte count as a readable size plus the exact number.
func formatBytes(n uint64) string {
	const unit = 1024
	exact := strconv.FormatUint(n, 10) + " ไบต์"
	if n < unit {
		return exact
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	v, i := float64(n)/unit, 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s (%s)", v, units[i], exact)
}
