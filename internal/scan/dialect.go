package scan

import "fmt"

// Dialect names the scheduling syntax an entry is written in.
//
// This exists because cron syntax is not one language, and the variants are
// mutually ambiguous rather than merely different. The same five-field string
// parses successfully under several dialects and means different things:
//
//	"0 0 * * 1"   vixie/POSIX cron -> Monday   (day-of-week 0-6, 0=Sunday)
//	"0 0 * * 1"   Quartz           -> Sunday   (day-of-week 1-7, 1=Sunday)
//
// Nothing about the string reveals which reading is correct. Only the engine
// that will execute it does. So dialect is recorded as a property of the
// *source* an entry was discovered from, never inferred from its text — a
// crontab line will be run by cron, a Hermes job by Hermes.
//
// Unsupported dialects are refused rather than parsed under cron semantics.
// Refusing loses a job WakeGuard could have woken for; guessing wakes the
// machine at confidently wrong times and reports success, which is worse.
type Dialect string

const (
	// DialectCron is POSIX/vixie five-field syntax plus @descriptors, as used
	// by crontab and, after translation, by launchd calendar intervals.
	DialectCron Dialect = "cron"

	// DialectInterval is a plain repeating period ("every 6h").
	DialectInterval Dialect = "interval"

	// The rest are recognised so a provider can declare them honestly, and so
	// the refusal message can name what it found. None are parsed.
	DialectQuartz     Dialect = "quartz"
	DialectJenkins    Dialect = "jenkins"
	DialectAWSEvents  Dialect = "aws-events"
	DialectSystemdCal Dialect = "systemd-calendar"
)

// Supported reports whether WakeGuard can evaluate this dialect correctly.
func (d Dialect) Supported() bool {
	return d == "" || d == DialectCron || d == DialectInterval
}

// UnsupportedReason explains a refusal in terms the user can act on.
func (d Dialect) UnsupportedReason() error {
	switch d {
	case DialectQuartz:
		return fmt.Errorf("Quartz cron syntax differs from POSIX cron " +
			"(day-of-week is 1-7 with 1=Sunday, and ?/L/# are supported), " +
			"so parsing it as POSIX cron would schedule wakes on the wrong days")
	case DialectJenkins:
		return fmt.Errorf("Jenkins cron uses H to spread load by hashing the job name, " +
			"so its fire times cannot be determined from the expression alone")
	case DialectAWSEvents:
		return fmt.Errorf("AWS EventBridge uses six fields with a required ?, " +
			"which does not map onto POSIX cron")
	case DialectSystemdCal:
		return fmt.Errorf("systemd OnCalendar syntax is not cron syntax " +
			"(for example 'Mon *-*-* 09:00:00') and needs its own parser")
	default:
		return fmt.Errorf("schedule dialect %q is not supported", d)
	}
}
