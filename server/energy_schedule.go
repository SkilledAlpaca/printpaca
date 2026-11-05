package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// energy_schedule.go implements energy.mode "schedule": a time-of-use tariff with seasons, weekday and
// weekend day types, named windows and holidays. A print runs continuously from its start; the span is split
// at every window edge, midnight and season change, and each piece costs hours x rate. web/src/lib/engine/
// schedule.ts mirrors this file; testdata/schedule-vectors.json pins both.

const (
	maxScheduleSeasons   = 6
	maxScheduleWindows   = 8
	maxScheduleHolidays  = 100
	maxScheduleNameChars = 40
	// scheduleSimulateHours bounds the segment walk. A longer span uses the average of its first 366 days for
	// the remainder (see scheduleEnergy).
	scheduleSimulateHours = 366 * 24
)

// TouWindow is a named rate that applies from Start up to End (wall-clock HH:MM; End may be 24:00).
type TouWindow struct {
	Name  string  `yaml:"name" json:"name"`
	Start string  `yaml:"start" json:"start"`
	End   string  `yaml:"end" json:"end"`
	Rate  float64 `yaml:"rate" json:"rate"`
}

// TouDay is one day type's schedule: hours outside every window cost BaseRate and report as BaseName.
type TouDay struct {
	BaseName string      `yaml:"baseName" json:"baseName"`
	BaseRate float64     `yaml:"baseRate" json:"baseRate"`
	Windows  []TouWindow `yaml:"windows" json:"windows"`
}

// TouSeason applies to the listed months (1-12). Weekend covers Saturday, Sunday and listed holidays.
type TouSeason struct {
	Name    string `yaml:"name" json:"name"`
	Months  []int  `yaml:"months" json:"months"`
	Weekday TouDay `yaml:"weekday" json:"weekday"`
	Weekend TouDay `yaml:"weekend" json:"weekend"`
}

// EnergySchedule is the energy.schedule block (also embedded, resolved, in a quote's EnergyInputs).
type EnergySchedule struct {
	DefaultStartTime string      `yaml:"defaultStartTime" json:"defaultStartTime"`
	Holidays         []string    `yaml:"holidays" json:"holidays"`
	Seasons          []TouSeason `yaml:"seasons" json:"seasons"`
	// SameWindowsAllYear is an editing constraint, not an input to pricing: every season has the same base
	// period names and the same windows (name, start, end) per day type, and only the rates differ. The
	// editor shows one set of windows when it is set. Omitted when false, so existing schedules store as before.
	SameWindowsAllYear bool `yaml:"sameWindowsAllYear,omitempty" json:"sameWindowsAllYear,omitempty"`
}

// EnergyPeriod is one named period's share of a schedule-priced print. Cost is in currency for the whole
// batch, unrounded.
type EnergyPeriod struct {
	Name  string  `json:"name"`
	Hours float64 `json:"hours"`
	Cost  float64 `json:"cost"`
}

var (
	hhmmRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// wallRe is a wall-clock date and time, "YYYY-MM-DDTHH:MM", read in the instance time zone.
	wallRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})T([01][0-9]|2[0-3]):([0-5][0-9])$`)
)

// parseClock reads HH:MM as minutes after midnight. allow24 accepts "24:00" (a window's end).
func parseClock(s string, allow24 bool) (int, bool) {
	if allow24 && s == "24:00" {
		return 1440, true
	}
	m := hhmmRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	var h, mi int
	fmt.Sscanf(m[1], "%d", &h)
	fmt.Sscanf(m[2], "%d", &mi)
	return h*60 + mi, true
}

// validDate reports whether s is a real calendar date written YYYY-MM-DD.
func validDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Format("2006-01-02") == s
}

func validateTouDay(path string, d TouDay) []string {
	var p []string
	if strings.TrimSpace(d.BaseName) == "" {
		p = append(p, path+".baseName: required")
	}
	if len([]rune(d.BaseName)) > maxScheduleNameChars {
		p = append(p, fmt.Sprintf("%s.baseName: at most %d characters", path, maxScheduleNameChars))
	}
	if !(d.BaseRate >= 0) || d.BaseRate > maxMoneyAmount {
		p = append(p, path+".baseRate: must be >= 0")
	}
	if len(d.Windows) > maxScheduleWindows {
		p = append(p, fmt.Sprintf("%s.windows: %d windows exceeds max %d", path, len(d.Windows), maxScheduleWindows))
		return p
	}
	type span struct{ s, e int }
	var spans []span
	for i, w := range d.Windows {
		wp := fmt.Sprintf("%s.windows[%d]", path, i)
		if strings.TrimSpace(w.Name) == "" {
			p = append(p, wp+".name: required")
		}
		if len([]rune(w.Name)) > maxScheduleNameChars {
			p = append(p, fmt.Sprintf("%s.name: at most %d characters", wp, maxScheduleNameChars))
		}
		if !(w.Rate >= 0) || w.Rate > maxMoneyAmount {
			p = append(p, wp+".rate: must be >= 0")
		}
		s, okS := parseClock(w.Start, false)
		e, okE := parseClock(w.End, true)
		if !okS {
			p = append(p, wp+".start: must be HH:MM")
		}
		if !okE {
			p = append(p, wp+".end: must be HH:MM (24:00 allowed)")
		}
		if okS && okE {
			if s >= e {
				p = append(p, wp+": start must be before end")
				continue
			}
			spans = append(spans, span{s, e})
		}
	}
	for i := range spans {
		for j := i + 1; j < len(spans); j++ {
			if spans[i].s < spans[j].e && spans[j].s < spans[i].e {
				p = append(p, path+".windows: windows must not overlap")
				return p
			}
		}
	}
	return p
}

// validateEnergySchedule checks the schedule's shape. A schedule with no seasons is valid only when
// requireSeasons is false (the unused default in non-schedule modes).
func validateEnergySchedule(s EnergySchedule, requireSeasons bool) []string {
	var p []string
	if _, ok := parseClock(s.DefaultStartTime, false); !ok {
		p = append(p, "energy.schedule.defaultStartTime: must be HH:MM")
	}
	if len(s.Holidays) > maxScheduleHolidays {
		p = append(p, fmt.Sprintf("energy.schedule.holidays: %d dates exceeds max %d", len(s.Holidays), maxScheduleHolidays))
	}
	for i, h := range s.Holidays {
		if !validDate(h) {
			p = append(p, fmt.Sprintf("energy.schedule.holidays[%d]: %q must be a date written YYYY-MM-DD", i, h))
		}
	}
	if len(s.Seasons) == 0 {
		if requireSeasons {
			p = append(p, "energy.schedule.seasons: add at least one season that covers months 1-12")
		}
		return p
	}
	if len(s.Seasons) > maxScheduleSeasons {
		p = append(p, fmt.Sprintf("energy.schedule.seasons: %d seasons exceeds max %d", len(s.Seasons), maxScheduleSeasons))
		return p
	}
	owner := map[int]string{}
	for i, se := range s.Seasons {
		sp := fmt.Sprintf("energy.schedule.seasons[%d]", i)
		if strings.TrimSpace(se.Name) == "" {
			p = append(p, sp+".name: required")
		}
		if len([]rune(se.Name)) > maxScheduleNameChars {
			p = append(p, fmt.Sprintf("%s.name: at most %d characters", sp, maxScheduleNameChars))
		}
		for _, m := range se.Months {
			if m < 1 || m > 12 {
				p = append(p, fmt.Sprintf("%s.months: %d is not a month 1-12", sp, m))
				continue
			}
			if prev, dup := owner[m]; dup {
				p = append(p, fmt.Sprintf("energy.schedule.seasons: month %d is in both %s and %s; a month belongs to one season", m, prev, sp))
				continue
			}
			owner[m] = sp
		}
		p = append(p, validateTouDay(sp+".weekday", se.Weekday)...)
		p = append(p, validateTouDay(sp+".weekend", se.Weekend)...)
	}
	if s.SameWindowsAllYear {
		p = append(p, validateSameWindows(s.Seasons)...)
	}
	var missing []string
	for m := 1; m <= 12; m++ {
		if _, ok := owner[m]; !ok {
			missing = append(missing, fmt.Sprint(m))
		}
	}
	if len(missing) > 0 {
		p = append(p, "energy.schedule.seasons: month(s) "+strings.Join(missing, ", ")+" are in no season; every month 1-12 needs one")
	}
	return p
}

// validateSameWindows reports each season whose base period names or windows (name, start, end) differ from
// the first season's, for a schedule with SameWindowsAllYear set. Rates may differ.
func validateSameWindows(seasons []TouSeason) []string {
	var p []string
	first := seasons[0]
	for i := 1; i < len(seasons); i++ {
		for _, dt := range []struct {
			name        string
			want, other TouDay
		}{{"weekday", first.Weekday, seasons[i].Weekday}, {"weekend", first.Weekend, seasons[i].Weekend}} {
			if !sameTouWindows(dt.want, dt.other) {
				p = append(p, fmt.Sprintf("energy.schedule.seasons[%d].%s.windows: sameWindowsAllYear is on, so the windows must match seasons[0] (rates may differ)", i, dt.name))
			}
		}
	}
	return p
}

func sameTouWindows(a, b TouDay) bool {
	if a.BaseName != b.BaseName || len(a.Windows) != len(b.Windows) {
		return false
	}
	for i, w := range a.Windows {
		if w.Name != b.Windows[i].Name || w.Start != b.Windows[i].Start || w.End != b.Windows[i].End {
			return false
		}
	}
	return true
}

func normalizeEnergySchedule(s *EnergySchedule) {
	if s.Holidays == nil {
		s.Holidays = []string{}
	}
	if s.Seasons == nil {
		s.Seasons = []TouSeason{}
	}
	for i := range s.Seasons {
		if s.Seasons[i].Months == nil {
			s.Seasons[i].Months = []int{}
		}
		for _, d := range []*TouDay{&s.Seasons[i].Weekday, &s.Seasons[i].Weekend} {
			if d.Windows == nil {
				d.Windows = []TouWindow{}
			}
		}
	}
}

// --- time zone arithmetic ---------------------------------------------------------------------------------
// Everything is float64 milliseconds since the epoch so the Go and TypeScript implementations do the same
// IEEE operations in the same order.

func zoneOffsetMs(loc *time.Location, ms float64) float64 {
	_, off := time.UnixMilli(int64(math.Floor(ms))).In(loc).Zone()
	return float64(off) * 1000
}

// wallToInstant converts a wall-clock date plus minutes after midnight (up to 1440 and beyond) in loc to an
// instant. Around a DST change it is deterministic: a repeated wall time resolves to the earlier instant, and
// a skipped one clamps to the first instant after the gap (02:30 on a spring-forward day becomes 03:00).
func wallToInstant(loc *time.Location, y, mo, d, minutes int) float64 {
	wall := float64(time.Date(y, time.Month(mo), d, 0, minutes, 0, 0, time.UTC).UnixMilli())
	oA := zoneOffsetMs(loc, wall-86400000)
	oB := zoneOffsetMs(loc, wall+86400000)
	tA, tB := wall-oA, wall-oB
	okA := zoneOffsetMs(loc, tA) == oA
	okB := zoneOffsetMs(loc, tB) == oB
	switch {
	case okA && okB:
		return math.Min(tA, tB)
	case okA:
		return tA
	case okB:
		return tB
	default:
		// Inside the spring-forward gap: tB (read with the later offset) is before the change and tA is
		// after it. Find the first instant whose offset is no longer oA by bisection to the millisecond.
		lo, hi := tB, tA
		for hi-lo > 1 {
			mid := math.Floor((lo + hi) / 2)
			if zoneOffsetMs(loc, mid) == oA {
				lo = mid
			} else {
				hi = mid
			}
		}
		return hi
	}
}

// parseWallStart reads "YYYY-MM-DDTHH:MM" in loc.
func parseWallStart(s string, loc *time.Location) (float64, bool) {
	m := wallRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	var y, mo, d, h, mi int
	fmt.Sscanf(m[1], "%d", &y)
	fmt.Sscanf(m[2], "%d", &mo)
	fmt.Sscanf(m[3], "%d", &d)
	fmt.Sscanf(m[4], "%d", &h)
	fmt.Sscanf(m[5], "%d", &mi)
	if !validDate(fmt.Sprintf("%04d-%02d-%02d", y, mo, d)) {
		return 0, false
	}
	return wallToInstant(loc, y, mo, d, h*60+mi), true
}

// nextStartWall is the next occurrence of the HH:MM clock at or after now (strictly later than now's minute),
// as a wall-clock "YYYY-MM-DDTHH:MM" in loc. It is the default print start.
func nextStartWall(clock string, loc *time.Location, now time.Time) string {
	if _, ok := parseClock(clock, false); !ok {
		clock = "22:00"
	}
	local := now.In(loc)
	today := local.Format("2006-01-02")
	if today+"T"+clock > local.Format("2006-01-02T15:04") {
		return today + "T" + clock
	}
	next := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, time.UTC)
	return next.Format("2006-01-02") + "T" + clock
}

// --- cost -------------------------------------------------------------------------------------------------

type scheduleDay struct {
	day    TouDay
	starts []int // sorted window edges, ending at 1440
	wins   []scWin
}

type scWin struct {
	s, e int
	name string
	rate float64
}

func buildScheduleDay(d TouDay) scheduleDay {
	sd := scheduleDay{day: d}
	set := map[int]bool{1440: true}
	for _, w := range d.Windows {
		s, _ := parseClock(w.Start, false)
		e, _ := parseClock(w.End, true)
		sd.wins = append(sd.wins, scWin{s, e, w.Name, w.Rate})
		set[s], set[e] = true, true
	}
	for b := range set {
		sd.starts = append(sd.starts, b)
	}
	// insertion sort: at most 17 edges
	for i := 1; i < len(sd.starts); i++ {
		for j := i; j > 0 && sd.starts[j] < sd.starts[j-1]; j-- {
			sd.starts[j], sd.starts[j-1] = sd.starts[j-1], sd.starts[j]
		}
	}
	return sd
}

// at returns the rate and period name at minute-of-day m, and the next edge after m.
func (sd scheduleDay) at(m int) (rate float64, name string, next int) {
	rate, name = sd.day.BaseRate, sd.day.BaseName
	for _, w := range sd.wins {
		if w.s <= m && m < w.e {
			rate, name = w.rate, w.name
			break
		}
	}
	next = 1440
	for _, b := range sd.starts {
		if b > m {
			next = b
			break
		}
	}
	return
}

// scheduleEnergy prices a continuous print of spanHours starting at the wall-clock start (in loc) on a printer
// drawing kW. It returns the periods in order of first use and the total cost. Spans past 366 days simulate
// the first 366 days and scale that average for the rest. ok is false when the schedule or start is unusable.
func scheduleEnergy(s EnergySchedule, loc *time.Location, startWall string, spanHours, kW float64) (periods []EnergyPeriod, total float64, ok bool) {
	if len(validateEnergySchedule(s, true)) > 0 {
		return nil, 0, false
	}
	startMs, good := parseWallStart(startWall, loc)
	if !good || math.IsNaN(spanHours) || math.IsNaN(kW) {
		return nil, 0, false
	}
	if spanHours <= 0 {
		return []EnergyPeriod{}, 0, true
	}
	simHours := math.Min(spanHours, scheduleSimulateHours)
	end := startMs + simHours*3600000

	seasonOf := map[int]int{}
	for i, se := range s.Seasons {
		for _, m := range se.Months {
			seasonOf[m] = i
		}
	}
	holiday := map[string]bool{}
	for _, h := range s.Holidays {
		holiday[h] = true
	}
	days := map[[2]int]scheduleDay{}

	type acc struct {
		name        string
		hours, rate float64 // rate here is the running sum of hours x rate
	}
	var order []*acc
	byName := map[string]*acc{}

	cur := startMs
	for cur < end {
		t := time.UnixMilli(int64(cur)).In(loc)
		y, mo, d := t.Year(), int(t.Month()), t.Day()
		msOfDay := ((t.Hour()*60+t.Minute())*60 + t.Second()) * 1000
		minute := msOfDay / 60000
		dateKey := fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
		wd := time.Date(y, time.Month(mo), d, 12, 0, 0, 0, time.UTC).Weekday()
		weekend := wd == time.Saturday || wd == time.Sunday || holiday[dateKey]
		si := seasonOf[mo]
		key := [2]int{si, 0}
		if weekend {
			key[1] = 1
		}
		sd, have := days[key]
		if !have {
			if weekend {
				sd = buildScheduleDay(s.Seasons[si].Weekend)
			} else {
				sd = buildScheduleDay(s.Seasons[si].Weekday)
			}
			days[key] = sd
		}
		rate, name, next := sd.at(minute)
		segEnd := wallToInstant(loc, y, mo, d, next)
		if segEnd <= cur {
			segEnd = cur + 60000
		}
		if segEnd > end {
			segEnd = end
		}
		hours := (segEnd - cur) / 3600000
		a := byName[name]
		if a == nil {
			a = &acc{name: name}
			byName[name] = a
			order = append(order, a)
		}
		a.hours += hours
		a.rate += hours * rate
		cur = segEnd
	}

	scale := spanHours / simHours
	periods = make([]EnergyPeriod, 0, len(order))
	var rateHours float64
	for _, a := range order {
		h, rh := a.hours*scale, a.rate*scale
		periods = append(periods, EnergyPeriod{Name: a.name, Hours: h, Cost: kW * rh})
		rateHours += rh
	}
	return periods, kW * rateHours, true
}

// scheduleInputWarnings validates a quote's schedule-mode energy block and returns the zone to read the
// start in. Problems are reported on the "schedule" and "printStart" fields so the UI can link to them.
func scheduleInputWarnings(e EnergyInputs) (*time.Location, []FieldWarning) {
	var out []FieldWarning
	bad := func(field, code, msg string) {
		out = append(out, FieldWarning{Field: field, Code: code, Message: msg, Severity: "error"})
	}
	loc := time.UTC
	if e.Schedule == nil {
		bad("schedule", "required", "schedule is required in schedule mode")
	} else if probs := validateEnergySchedule(*e.Schedule, true); len(probs) > 0 {
		bad("schedule", "invalid", "schedule: "+probs[0])
	}
	if l, err := loadTimezone(e.Timezone); err != nil {
		bad("schedule", "invalid", "timezone: "+strconv.Quote(e.Timezone)+" is not an IANA time zone name")
	} else {
		loc = l
	}
	if e.Start == "" {
		bad("printStart", "required", "Enter the print start")
	} else if _, ok := parseWallStart(e.Start, loc); !ok {
		bad("printStart", "invalid", "Print start must be a date and time such as 2026-12-24T22:00")
	}
	return loc, out
}

// readableStart renders a wall-clock start ("2026-11-04T22:00") the way a person reads it ("Nov 4, 2026 10:00
// PM"); anything that doesn't parse is shown as given.
func readableStart(start string) string {
	t, err := time.Parse("2006-01-02T15:04", start)
	if err != nil {
		return start
	}
	return t.Format("Jan 2, 2006 3:04 PM")
}

func electricityBatch(mode string, perUnit, qty, scheduleBatch float64) float64 {
	if mode == "schedule" {
		return scheduleBatch
	}
	return perUnit * qty
}

func electricityFormula(in QuoteInputs, hours, rate float64, periods []EnergyPeriod) string {
	if in.Energy.Mode != "schedule" {
		return formatf(round2(hours)) + " h × " + formatf(in.Watts) + " W ÷ 1000 × " + formatf(rate) + "/kWh"
	}
	if len(periods) == 0 {
		// Name the missing start rather than blaming the schedule: an empty start is the common cause.
		if strings.TrimSpace(in.Energy.Start) == "" {
			return "needs a print start"
		}
		return "schedule not usable"
	}
	parts := make([]string, len(periods))
	for i, p := range periods {
		parts[i] = formatf(round2(p.Hours)) + " h " + p.Name
	}
	return formatf(in.Watts) + " W ÷ 1000 × schedule from " + readableStart(in.Energy.Start) + " (" + strings.Join(parts, ", ") + ")"
}

func electricityInputs(in QuoteInputs, hours, rate float64) map[string]any {
	m := map[string]any{"hours": round2(hours), "watts": float64(in.Watts), "rate": rate}
	if in.Energy.Mode == "schedule" {
		m["start"] = in.Energy.Start
		m["timezone"] = in.Energy.Timezone
	}
	return m
}
