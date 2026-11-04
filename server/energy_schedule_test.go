package main

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

type scheduleVectorFile struct {
	Description string                    `json:"description"`
	Schedules   map[string]EnergySchedule `json:"schedules"`
	Vectors     []scheduleVector          `json:"vectors"`
	NextStart   []nextStartVector         `json:"nextStart"`
}

type scheduleVector struct {
	Name         string        `json:"name"`
	Schedule     string        `json:"schedule"`
	Timezone     string        `json:"timezone"`
	Start        string        `json:"start"`
	HoursPerUnit float64       `json:"hoursPerUnit"`
	Quantity     float64       `json:"quantity"`
	Watts        float64       `json:"watts"`
	Holidays     []string      `json:"holidays,omitempty"`
	Expected     *scheduleWant `json:"expected,omitempty"`
}

type scheduleWant struct {
	Total   float64        `json:"total"`
	Periods []EnergyPeriod `json:"periods"`
}

type nextStartVector struct {
	Name     string `json:"name"`
	Clock    string `json:"clock"`
	Timezone string `json:"timezone"`
	Now      string `json:"now"`
	Expected string `json:"expected"`
}

const scheduleVectorPath = "../testdata/schedule-vectors.json"

func loadScheduleVectors(t *testing.T) scheduleVectorFile {
	t.Helper()
	raw, err := os.ReadFile(scheduleVectorPath)
	if err != nil {
		t.Fatal(err)
	}
	var f scheduleVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func runScheduleVector(t *testing.T, f scheduleVectorFile, v scheduleVector) scheduleWant {
	t.Helper()
	s, ok := f.Schedules[v.Schedule]
	if !ok {
		t.Fatalf("unknown schedule %q", v.Schedule)
	}
	if v.Holidays != nil {
		s.Holidays = v.Holidays
	}
	loc, err := loadTimezone(v.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	periods, total, ok := scheduleEnergy(s, loc, v.Start, v.HoursPerUnit*v.Quantity, v.Watts/1000)
	if !ok {
		t.Fatalf("scheduleEnergy rejected %q", v.Name)
	}
	return scheduleWant{Total: total, Periods: periods}
}

func TestScheduleVectors(t *testing.T) {
	f := loadScheduleVectors(t)
	if os.Getenv("UPDATE_SCHEDULE_VECTORS") == "1" {
		for i := range f.Vectors {
			w := runScheduleVector(t, f, f.Vectors[i])
			f.Vectors[i].Expected = &w
		}
		out, _ := json.MarshalIndent(f, "", " ")
		if err := os.WriteFile(scheduleVectorPath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if len(f.Vectors) < 10 {
		t.Fatalf("only %d vectors", len(f.Vectors))
	}
	for _, v := range f.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			if v.Expected == nil {
				t.Fatal("vector has no expected; run with UPDATE_SCHEDULE_VECTORS=1")
			}
			got := runScheduleVector(t, f, v)
			if math.Abs(got.Total-v.Expected.Total) > 1e-9 {
				t.Errorf("total = %v, want %v", got.Total, v.Expected.Total)
			}
			if len(got.Periods) != len(v.Expected.Periods) {
				t.Fatalf("periods = %+v, want %+v", got.Periods, v.Expected.Periods)
			}
			for i, p := range got.Periods {
				w := v.Expected.Periods[i]
				if p.Name != w.Name || math.Abs(p.Hours-w.Hours) > 1e-9 || math.Abs(p.Cost-w.Cost) > 1e-9 {
					t.Errorf("period %d = %+v, want %+v", i, p, w)
				}
			}
		})
	}
}

func TestNextStartWallVectors(t *testing.T) {
	for _, v := range loadScheduleVectors(t).NextStart {
		t.Run(v.Name, func(t *testing.T) {
			loc, err := loadTimezone(v.Timezone)
			if err != nil {
				t.Fatal(err)
			}
			now, err := time.Parse(time.RFC3339, v.Now)
			if err != nil {
				t.Fatal(err)
			}
			if got := nextStartWall(v.Clock, loc, now); got != v.Expected {
				t.Errorf("got %s, want %s", got, v.Expected)
			}
		})
	}
}

// Hand-checked totals, independent of the fixture's generated numbers.
func TestScheduleHandChecked(t *testing.T) {
	f := loadScheduleVectors(t)
	cases := map[string]float64{
		"winter weekday super off-peak only":          2 * 0.28,
		"summer weekday spans off-peak and on-peak":   3*0.33 + 5*0.55,
		"across midnight into a weekend":              1*0.55 + 7*0.33,
		"weekend mid-peak":                            1*0.33 + 2*0.44,
		"across the summer to winter season boundary": 1*0.55 + 3*0.33 + 4*0.34,
		"holiday uses the weekend schedule":           1*0.33 + 2*0.44,
		"quantity 3 runs back to back":                0.8 * (2.5*0.33 + 5*0.55),
		"spring forward DST change":                   9*0.34 + 3*0.28,
		"fall back DST change":                        11*0.34 + 1*0.28,
		"single flat season in UTC":                   0.25 * 5 * 0.2,
	}
	for _, v := range f.Vectors {
		want, ok := cases[v.Name]
		if !ok {
			continue
		}
		got := runScheduleVector(t, f, v)
		if math.Abs(got.Total-want) > 1e-9 {
			t.Errorf("%s: total = %v, want %v", v.Name, got.Total, want)
		}
	}
}

func TestValidateEnergySchedule(t *testing.T) {
	f := loadScheduleVectors(t)
	base := func() EnergySchedule {
		raw, _ := json.Marshal(f.Schedules["sce"])
		var s EnergySchedule
		_ = json.Unmarshal(raw, &s)
		return s
	}
	if p := validateEnergySchedule(base(), true); len(p) != 0 {
		t.Fatalf("sce example should validate: %v", p)
	}
	cases := []struct {
		name   string
		mutate func(s *EnergySchedule)
		want   string
	}{
		{"month in no season", func(s *EnergySchedule) { s.Seasons[1].Months = s.Seasons[1].Months[1:] }, "month(s) 1 are in no season"},
		{"month in two seasons", func(s *EnergySchedule) { s.Seasons[1].Months = append(s.Seasons[1].Months, 6) }, "month 6 is in both"},
		{"month out of range", func(s *EnergySchedule) { s.Seasons[0].Months = append(s.Seasons[0].Months, 13) }, "13 is not a month"},
		{"overlap", func(s *EnergySchedule) {
			w := &s.Seasons[1].Weekday.Windows
			*w = append(*w, TouWindow{Name: "X", Start: "15:00", End: "17:00", Rate: 1})
		}, "must not overlap"},
		{"start not before end", func(s *EnergySchedule) { s.Seasons[0].Weekday.Windows[0].Start = "21:00" }, "start must be before end"},
		{"bad time", func(s *EnergySchedule) { s.Seasons[0].Weekday.Windows[0].Start = "4pm" }, "start: must be HH:MM"},
		{"24:00 end is fine as start is not", func(s *EnergySchedule) { s.Seasons[0].Weekday.Windows[0].Start = "24:00" }, "start: must be HH:MM"},
		{"negative rate", func(s *EnergySchedule) { s.Seasons[0].Weekday.BaseRate = -1 }, "baseRate"},
		{"negative window rate", func(s *EnergySchedule) { s.Seasons[0].Weekday.Windows[0].Rate = -0.1 }, "rate: must be >= 0"},
		{"bad holiday", func(s *EnergySchedule) { s.Holidays = []string{"2026-02-30"} }, "holidays[0]"},
		{"bad default start", func(s *EnergySchedule) { s.DefaultStartTime = "25:00" }, "defaultStartTime"},
		{"too many seasons", func(s *EnergySchedule) {
			for len(s.Seasons) <= maxScheduleSeasons {
				s.Seasons = append(s.Seasons, s.Seasons[0])
			}
		}, "exceeds max 6"},
		{"too many windows", func(s *EnergySchedule) {
			d := &s.Seasons[0].Weekday
			d.Windows = nil
			for i := 0; i <= maxScheduleWindows; i++ {
				d.Windows = append(d.Windows, TouWindow{Name: "w", Start: "00:00", End: "01:00", Rate: 1})
			}
		}, "exceeds max 8"},
		{"too many holidays", func(s *EnergySchedule) {
			for i := 0; i <= maxScheduleHolidays; i++ {
				s.Holidays = append(s.Holidays, "2026-01-01")
			}
		}, "holidays: 101"},
		{"missing base name", func(s *EnergySchedule) { s.Seasons[0].Weekend.BaseName = " " }, "baseName: required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base()
			c.mutate(&s)
			p := strings.Join(validateEnergySchedule(s, true), "\n")
			if !strings.Contains(p, c.want) {
				t.Errorf("problems %q do not contain %q", p, c.want)
			}
		})
	}
	if p := validateEnergySchedule(EnergySchedule{DefaultStartTime: "22:00"}, true); len(p) == 0 {
		t.Error("schedule mode with no seasons should fail")
	}
	if p := validateEnergySchedule(EnergySchedule{DefaultStartTime: "22:00"}, false); len(p) != 0 {
		t.Errorf("empty unused schedule should pass: %v", p)
	}
	end := base()
	end.Seasons[0].Weekday.Windows[0].End = "24:00"
	if p := validateEnergySchedule(end, true); len(p) != 0 {
		t.Errorf("end 24:00 should pass: %v", p)
	}
}

func scheduleQuoteInputs(t *testing.T, start string) QuoteInputs {
	t.Helper()
	f := loadScheduleVectors(t)
	s := f.Schedules["sce"]
	return QuoteInputs{
		WeightG: 100, PricePerKg: 20, TimeHours: 8, TimeMinutes: 0, Watts: 1000,
		Energy:   EnergyInputs{Mode: "schedule", Schedule: &s, Timezone: "America/Los_Angeles", Start: start},
		Quantity: 1, MachineLifeHours: 4000, MachineCost: 400, MonthlyHours: 100,
	}
}

func TestComputeQuoteScheduleElectricity(t *testing.T) {
	// 2026-07-03T14:00 for 8 h in the "summer weekday spans off-peak and on-peak" shape: 3 h off-peak at
	// 0.33, 5 h on-peak at 0.55 (kW = 1) = 3.74 -> 374 cents.
	in := scheduleQuoteInputs(t, "2026-07-03T14:00")
	bd := ComputeQuote(in, DefaultEngineOptions)
	if !bd.Valid {
		t.Fatalf("invalid: %+v", bd.Warnings)
	}
	el := bd.PerUnit[1]
	if el.ID != "electricity" || el.Amount != 374 {
		t.Fatalf("electricity = %+v, want 374 cents", el)
	}
	if len(el.Periods) != 2 || el.Periods[0].Name != "Off-peak" || el.Periods[1].Name != "On-peak" {
		t.Errorf("periods = %+v", el.Periods)
	}
	if !strings.Contains(el.Formula, "On-peak") {
		t.Errorf("formula %q should name the periods", el.Formula)
	}

	// Quantity 2: the batch is 16 h from the start, and the per-unit electricity is half the batch.
	in.Quantity = 2
	bd = ComputeQuote(in, DefaultEngineOptions)
	// 14:00 -> 06:00 next day: 3 h + 5 h... = off 2h, on 5h, off 3h, then Saturday 00:00-06:00 off 6h.
	// off-peak 11 h x 0.33 + on-peak 5 h x 0.55 = 6.38 -> batch 638 cents.
	want := int64(math.Round((11*0.33 + 5*0.55) * 100))
	if got := bd.PerUnit[1].Amount; got != want/2 {
		t.Errorf("per-unit electricity = %d, want %d", got, want/2)
	}
	if bd.BatchTotal < want {
		t.Errorf("batch total %d should include the %d electricity batch", bd.BatchTotal, want)
	}

	// Same inputs, different start, different price: the start matters.
	other := scheduleQuoteInputs(t, "2026-07-03T22:00")
	if ComputeQuote(other, DefaultEngineOptions).PerUnit[1].Amount == bd.PerUnit[1].Amount {
		t.Error("start did not change the price")
	}
}

func TestComputeQuoteScheduleInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *QuoteInputs)
		field  string
	}{
		{"missing schedule", func(in *QuoteInputs) { in.Energy.Schedule = nil }, "schedule"},
		{"missing start", func(in *QuoteInputs) { in.Energy.Start = "" }, "printStart"},
		{"malformed start", func(in *QuoteInputs) { in.Energy.Start = "tomorrow" }, "printStart"},
		{"impossible date", func(in *QuoteInputs) { in.Energy.Start = "2026-02-30T10:00" }, "printStart"},
		{"bad zone", func(in *QuoteInputs) { in.Energy.Timezone = "Mars/Base" }, "schedule"},
		{"broken schedule", func(in *QuoteInputs) {
			s := *in.Energy.Schedule
			s.Seasons = s.Seasons[:1]
			in.Energy.Schedule = &s
		}, "schedule"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := scheduleQuoteInputs(t, "2026-07-03T14:00")
			c.mutate(&in)
			bd := ComputeQuote(in, DefaultEngineOptions)
			if bd.Valid {
				t.Fatal("expected invalid")
			}
			found := false
			for _, w := range bd.Warnings {
				if w.Field == c.field && w.Severity == "error" {
					found = true
				}
			}
			if !found {
				t.Errorf("no error on %q: %+v", c.field, bd.Warnings)
			}
		})
	}
}

// A saved quote carries its schedule and start, so repricing after the shop's settings change is identical.
func TestScheduleQuoteRoundTripsThroughJSON(t *testing.T) {
	in := scheduleQuoteInputs(t, "2026-07-03T14:00")
	raw, err := json.Marshal(OrderInputs{Parts: []OrderPartInputs{{Label: "p", QuoteInputs: in}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := computeQuoteOutputs(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := ComputeQuote(in, DefaultEngineOptions)
	if out.BatchTotalCents != want.BatchTotal || !out.Valid {
		t.Errorf("recomputed %d valid=%v, want %d", out.BatchTotalCents, out.Valid, want.BatchTotal)
	}
}

func TestPublicQuoteInputsUseNextDefaultStart(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := loadScheduleVectors(t)
	cfg.Energy.Mode = "schedule"
	cfg.Energy.Schedule = f.Schedules["sce"]
	cfg.Timezone = "America/Los_Angeles"
	mat := cfg.Materials[0]
	qi, opt := buildPublicQuoteInputs(cfg, mat, 1, 50, 3600*3)
	if qi.Energy.Mode != "schedule" || qi.Energy.Schedule == nil || qi.Energy.Timezone != "America/Los_Angeles" {
		t.Fatalf("energy = %+v", qi.Energy)
	}
	if !strings.HasSuffix(qi.Energy.Start, "T22:00") {
		t.Errorf("start = %q, want the next 22:00", qi.Energy.Start)
	}
	if bd := ComputeQuote(qi, opt); !bd.Valid || bd.Pricing == nil || !bd.Pricing.Valid {
		t.Errorf("public schedule quote invalid: %+v", bd.Warnings)
	}
}

func TestConfigScheduleMode(t *testing.T) {
	f := loadScheduleVectors(t)
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Energy.Schedule.DefaultStartTime != "22:00" || cfg.Energy.Schedule.Seasons == nil || cfg.Energy.Schedule.Holidays == nil {
		t.Errorf("defaults: %+v", cfg.Energy.Schedule)
	}
	cfg.Energy.Mode = "schedule"
	if p := validate(cfg); !strings.Contains(strings.Join(p, "\n"), "energy.schedule.seasons") {
		t.Errorf("schedule mode with no seasons should be rejected: %v", p)
	}
	cfg.Energy.Schedule = f.Schedules["sce"]
	if p := validate(cfg); len(p) != 0 {
		t.Errorf("sce example should validate: %v", p)
	}
}

// A wall time inside the spring-forward gap clamps to the first instant after it, like the TypeScript engine.
func TestWallToInstantClampsSpringForwardGap(t *testing.T) {
	loc, err := loadTimezone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	want := float64(time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC).UnixMilli())
	if got := wallToInstant(loc, 2026, 3, 8, 150); got != want {
		t.Errorf("wallToInstant(02:30) = %v, want %v (03:00 PDT)", got, want)
	}
	if got := wallToInstant(loc, 2026, 3, 8, 120); got != want {
		t.Errorf("wallToInstant(02:00) = %v, want %v (03:00 PDT)", got, want)
	}
}

// A blank print start is the user's omission, so the Electricity line says so instead of blaming the schedule.
func TestElectricityFormulaNamesMissingPrintStart(t *testing.T) {
	missing := ComputeQuote(scheduleQuoteInputs(t, ""), DefaultEngineOptions)
	if got := missing.PerUnit[1].Formula; got != "needs a print start" {
		t.Errorf("blank start formula = %q, want %q", got, "needs a print start")
	}
	bad := ComputeQuote(scheduleQuoteInputs(t, "not-a-date"), DefaultEngineOptions)
	if got := bad.PerUnit[1].Formula; got != "schedule not usable" {
		t.Errorf("invalid start formula = %q, want %q", got, "schedule not usable")
	}
}

func TestElectricityFormulaShowsReadableStart(t *testing.T) {
	bd := ComputeQuote(scheduleQuoteInputs(t, "2026-07-03T14:00"), DefaultEngineOptions)
	if got := bd.PerUnit[1].Formula; !strings.Contains(got, "schedule from Jul 3, 2026 2:00 PM (") {
		t.Errorf("formula %q should show the start as Jul 3, 2026 2:00 PM", got)
	}
	if got := readableStart("garbage"); got != "garbage" {
		t.Errorf("readableStart(garbage) = %q", got)
	}
}
