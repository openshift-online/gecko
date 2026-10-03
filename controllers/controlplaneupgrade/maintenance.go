package controlplaneupgrade

import (
	"fmt"
	"time"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
)

// maintenancePermitted evaluates whether an upgrade may start at the given time
// according to the cluster's maintenance policy. It returns true if no policy is
// set (upgrades permitted at any time), or if the current time falls within a
// maintenance window and outside all exclusions.
func maintenancePermitted(policy *privatev1.ControlPlaneUpgradePolicy, now time.Time) (permitted bool, reason, message string, nextWindow *time.Time) {
	if policy == nil || policy.Spec.MaintenanceWindow == nil {
		// No policy or no window defined: upgrades permitted at any time
		return true, "", "", nil
	}

	window := policy.Spec.MaintenanceWindow

	// Check if we're currently in an exclusion period
	for _, exclusion := range policy.Spec.MaintenanceExclusions {
		start := exclusion.Start.Time
		end := exclusion.End.Time
		if !now.Before(start) && now.Before(end) {
			return false, "MaintenanceExclusionActive",
				fmt.Sprintf("Upgrade blocked by maintenance exclusion %q (ends %s)", exclusion.Name, end.UTC().Format(time.RFC3339)),
				nil
		}
	}

	// Check if we're currently in a maintenance window
	inWindow, next := inMaintenanceWindow(window, now)
	if inWindow {
		return true, "", "", nil
	}

	// Not in window, not in exclusion: wait for next window
	message = "Waiting for next maintenance window"
	if next != nil {
		message = fmt.Sprintf("Next maintenance window opens at %s", next.UTC().Format(time.RFC3339))
	}
	return false, "OutsideMaintenanceWindow", message, next
}

// inMaintenanceWindow reports whether now falls within a maintenance window
// occurrence, and returns the next window start time if not currently in one.
func inMaintenanceWindow(window *privatev1.ControlPlaneMaintenanceWindow, now time.Time) (inWindow bool, nextStart *time.Time) {
	if window == nil {
		return false, nil
	}

	// Convert window start to UTC
	start := window.Start.Time.UTC()
	duration := time.Duration(window.DurationMinutes) * time.Minute

	// Simple weekly recurrence based on days of week
	// This is a simplified implementation; a full RFC 5545 parser would be needed
	// for production use. For now, we support the common case: weekly recurrence
	// on specified days.
	if window.Recurrence.Frequency != "weekly" {
		// Unsupported frequency: treat as no window (permitted at any time)
		return true, nil
	}

	// Build a map of allowed weekdays
	allowedDays := make(map[time.Weekday]bool)
	for _, dayName := range window.Recurrence.DaysOfWeek {
		if day := parseWeekday(dayName); day >= 0 {
			allowedDays[day] = true
		}
	}

	if len(allowedDays) == 0 {
		// No valid days: treat as no window
		return true, nil
	}

	// Check if today is an allowed day
	nowUTC := now.UTC()
	if !allowedDays[nowUTC.Weekday()] {
		// Not an allowed day: find next allowed day
		next := findNextWindowStart(nowUTC, allowedDays, start, duration)
		return false, &next
	}

	// Today is an allowed day: check if we're within the window time range
	// Extract the time-of-day from the window start
	startTime := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(),
		start.Hour(), start.Minute(), start.Second(), 0, time.UTC)
	endTime := startTime.Add(duration)

	if !nowUTC.Before(startTime) && nowUTC.Before(endTime) {
		// Currently in window
		return true, nil
	}

	// Today is allowed but we're outside the time range
	if nowUTC.Before(startTime) {
		// Before today's window: next window is today
		return false, &startTime
	}

	// After today's window: find next allowed day
	next := findNextWindowStart(nowUTC, allowedDays, start, duration)
	return false, &next
}

// parseWeekday converts a day name to time.Weekday
func parseWeekday(name string) time.Weekday {
	switch name {
	case "sunday":
		return time.Sunday
	case "monday":
		return time.Monday
	case "tuesday":
		return time.Tuesday
	case "wednesday":
		return time.Wednesday
	case "thursday":
		return time.Thursday
	case "friday":
		return time.Friday
	case "saturday":
		return time.Saturday
	default:
		return -1
	}
}

// findNextWindowStart finds the next maintenance window start time after now
func findNextWindowStart(now time.Time, allowedDays map[time.Weekday]bool, windowStart time.Time, duration time.Duration) time.Time {
	// Start checking from tomorrow
	candidate := now.AddDate(0, 0, 1)

	// Search up to 7 days ahead (one full week)
	for i := 0; i < 7; i++ {
		if allowedDays[candidate.Weekday()] {
			// Found next allowed day: return window start time on that day
			return time.Date(candidate.Year(), candidate.Month(), candidate.Day(),
				windowStart.Hour(), windowStart.Minute(), windowStart.Second(), 0, time.UTC)
		}
		candidate = candidate.AddDate(0, 0, 1)
	}

	// Shouldn't reach here if allowedDays is non-empty, but return a week from now as fallback
	return now.AddDate(0, 0, 7)
}

// overrideCondition checks if maintenance windows/exclusions should be bypassed
// due to EOL proximity or critical security/platform issues.
func overrideCondition(cluster *privatev1.Cluster, isYStream bool) (override bool, reason, message string) {
	// Check for critical security or platform flag on the cluster
	// This would be set by platform operators for emergency patches
	if cluster.Annotations != nil {
		if cluster.Annotations["gecko.openshift.io/critical-upgrade"] == "true" {
			return true, "CriticalUpgradeRequired",
				"Critical security or platform issue requires immediate patching"
		}
	}

	// Y-stream only: check EOL proximity
	// TODO: This requires EOL date metadata, which would come from the Version API
	// or Channel status. For now, this is a placeholder for the future implementation.
	// The EOL check would look like:
	// if isYStream && cluster.Status.HostedClusterResult != nil {
	//     currentVersion := cluster.Status.HostedClusterResult.Version
	//     eolDate := getEOLDate(currentVersion) // from Version API
	//     if time.Until(eolDate) < 30*24*time.Hour {
	//         return true, "ApproachingEOL",
	//             fmt.Sprintf("Version %s reaches EOL on %s", currentVersion, eolDate.Format("2006-01-02"))
	//     }
	// }

	return false, "", ""
}
