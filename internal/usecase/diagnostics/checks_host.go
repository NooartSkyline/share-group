package diagnostics

import (
	"context"
	"fmt"
	"math"

	"lanshare/internal/domain"
)

// checkDisk compares the free space of the data volume with the free-space
// reserve R: below R fails, from R up to (not including) 4R warns, and 4R or
// more is fine. The free space and the bytes held by unfinished uploads are
// shown in every case.
func (s *Service) checkDisk(ctx context.Context, _ runEnv) domain.CheckResult {
	m := metaDisk
	if s.deps.Disk == nil {
		return m.unknownFrom(fmt.Errorf("diagnostics: no disk reader"), "ข้อมูลพื้นที่ดิสก์", "")
	}
	free, err := s.deps.Disk.Free(ctx)
	if err != nil {
		return m.unknownFrom(err, "ข้อมูลพื้นที่ดิสก์", "")
	}

	var partial uint64
	if s.deps.Partials != nil {
		if n := s.deps.Partials.PartialBytes(); n > 0 {
			partial = uint64(n)
		}
	}
	reserve := s.deps.MinFree
	detail := fmt.Sprintf("พื้นที่ว่าง %s, ขั้นต่ำที่ต้องเหลือ %s, ไฟล์ที่ยังส่งไม่จบ %s",
		formatBytes(free), formatBytes(reserve), formatBytes(partial))

	switch {
	case free < reserve:
		return m.fail(detail,
			"พื้นที่ว่างต่ำกว่าขั้นต่ำ ระบบจะปฏิเสธไฟล์ใหม่ ให้ลบไฟล์ที่ไม่ใช้หรือเพิ่มพื้นที่ว่างบน Mac ก่อน")
	case free < satMul4(reserve):
		return m.warn(detail,
			"พื้นที่ว่างเหลือไม่มาก ควรลบไฟล์ที่ไม่ใช้หรือเพิ่มพื้นที่ว่างก่อนรับไฟล์ขนาดใหญ่")
	}
	return m.ok(detail)
}

// satMul4 returns 4*v, saturating at the largest uint64.
func satMul4(v uint64) uint64 {
	if v > math.MaxUint64/4 {
		return math.MaxUint64
	}
	return v * 4
}

// checkPower reports whether the server keeps the Mac awake during transfers.
func (s *Service) checkPower(_ context.Context, _ runEnv) domain.CheckResult {
	m := metaPower
	if s.deps.Power == nil {
		return m.unknownFrom(fmt.Errorf("diagnostics: no power guard"), "สถานะการกันหลับ", "")
	}
	const sleepHint = "ตั้งค่าไม่ให้ Mac หลับเองใน System Settings > Battery หรือ Lock Screen ระหว่างส่งไฟล์ใหญ่ และเสียบสายชาร์จไว้"
	switch st := s.deps.Power.Status(); st {
	case domain.PowerActive:
		return m.ok("สถานะ active: กำลังกัน Mac ไม่ให้หลับระหว่างถ่ายโอนไฟล์")
	case domain.PowerIdle:
		return m.ok("สถานะ idle: เปิดการกันหลับไว้ และจะเริ่มทำงานเมื่อมีการถ่ายโอนไฟล์")
	case domain.PowerDisabled:
		return m.warn(
			"สถานะ disabled: ปิดการกันหลับด้วย -keep-awake=false Mac อาจหลับระหว่างถ่ายโอนไฟล์",
			"เริ่มเซิร์ฟเวอร์โดยไม่ใส่ -keep-awake=false หรือ "+sleepHint)
	case domain.PowerUnavailable:
		return m.warn(
			"สถานะ unavailable: เริ่มโปรแกรม caffeinate ไม่ได้ จึงกัน Mac หลับไม่ได้",
			sleepHint)
	default:
		return m.unknown(ReasonParseFailed, "ไม่รู้จักสถานะการกันหลับที่ได้รับ", "")
	}
}

// secureContextResult depends on how this request reached the server, so it
// is built per request and never taken from the cache.
func secureContextResult(secure bool) domain.CheckResult {
	m := metaSecureContext
	if secure {
		return m.ok("เชื่อมต่อผ่าน HTTPS")
	}
	return m.warn(
		"เปิดผ่าน HTTP ธรรมดา: Service Worker และการแจ้งเตือนของระบบจะใช้ไม่ได้ในอุปกรณ์อื่นจนกว่าจะมี HTTPS",
		"การแชร์ไฟล์ยังใช้งานได้ตามปกติ การติดตั้งแอปแบบเต็มและการแจ้งเตือนของระบบต้องรอ HTTPS")
}
