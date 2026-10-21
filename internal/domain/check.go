package domain

// CheckStatus is the outcome of one connectivity or environment check.
type CheckStatus string

const (
	CheckOK      CheckStatus = "ok"
	CheckWarn    CheckStatus = "warn"
	CheckFail    CheckStatus = "fail"
	CheckUnknown CheckStatus = "unknown" // the check could not run or its output was not understood
)

// CheckResult is one line of the owner's diagnostics report.
//
// Invariant: it never carries a PIN, token, resume key or file name, because
// the report is meant to be copied and pasted into support conversations.
type CheckResult struct {
	ID     string
	Status CheckStatus
	Title  string
	Detail string
	Hint   string
	// Reason is a stable English code explaining a warn/unknown outcome
	// (for example "command_missing" or "timeout"); Detail is the Thai text.
	Reason string
}

// PowerStatus tells whether the server is currently keeping the Mac awake.
type PowerStatus string

const (
	PowerActive      PowerStatus = "active"      // holding the sleep assertion
	PowerIdle        PowerStatus = "idle"        // enabled, but no transfer is running
	PowerDisabled    PowerStatus = "disabled"    // turned off by configuration
	PowerUnavailable PowerStatus = "unavailable" // the assertion could not be started
)
