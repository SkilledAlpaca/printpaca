package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
)

// Fake Spoolman/Moonraker data that registerProxies mounts at the real proxies' paths when
// DEMO_MODE=1 and the *_URL is unset, so the SPA clients need no change. GET-only static in-memory
// data: no filesystem or database access.

// demoSpoolVendor mirrors web/src/lib/spoolman.ts's SpoolDto.filament.vendor.
type demoSpoolVendor struct {
	Name string `json:"name"`
}

// demoSpoolFilament mirrors web/src/lib/spoolman.ts's SpoolDto.filament.
type demoSpoolFilament struct {
	Name     string          `json:"name"`
	Material string          `json:"material"`
	Density  float64         `json:"density"`
	Weight   float64         `json:"weight"`
	Price    float64         `json:"price"`
	Vendor   demoSpoolVendor `json:"vendor"`
}

// demoSpool mirrors the fields of SpoolDto that the app reads.
type demoSpool struct {
	ID              int               `json:"id"`
	RemainingWeight float64           `json:"remaining_weight"`
	Price           float64           `json:"price"`
	InitialWeight   float64           `json:"initial_weight"`
	Archived        bool              `json:"archived"`
	Filament        demoSpoolFilament `json:"filament"`
}

// demoSpools is a fixed set of plausible spools; Price/InitialWeight give a sane $/kg in spoolPricePerKg.
var demoSpools = []demoSpool{
	{
		ID: 1001, RemainingWeight: 850, Price: 22.99, InitialWeight: 1000,
		Filament: demoSpoolFilament{Name: "Matte Black", Material: "PLA", Density: 1.24, Weight: 1000, Price: 22.99,
			Vendor: demoSpoolVendor{Name: "Polymaker"}},
	},
	{
		ID: 1002, RemainingWeight: 410, Price: 24.99, InitialWeight: 1000,
		Filament: demoSpoolFilament{Name: "Galaxy Blue", Material: "PETG", Density: 1.27, Weight: 1000, Price: 24.99,
			Vendor: demoSpoolVendor{Name: "Prusament"}},
	},
	{
		ID: 1003, RemainingWeight: 980, Price: 27.99, InitialWeight: 1000,
		Filament: demoSpoolFilament{Name: "Fire Engine Red", Material: "ABS", Density: 1.04, Weight: 1000, Price: 27.99,
			Vendor: demoSpoolVendor{Name: "Hatchbox"}},
	},
	{
		ID: 1004, RemainingWeight: 120, Price: 21.99, InitialWeight: 1000,
		Filament: demoSpoolFilament{Name: "Silk Gold", Material: "PLA", Density: 1.24, Weight: 1000, Price: 21.99,
			Vendor: demoSpoolVendor{Name: "eSun"}},
	},
	{
		ID: 1005, RemainingWeight: 650, Price: 25.99, InitialWeight: 1000,
		Filament: demoSpoolFilament{Name: "Carbon Fiber Black", Material: "PETG", Density: 1.29, Weight: 1000, Price: 25.99,
			Vendor: demoSpoolVendor{Name: "Overture"}},
	},
	{
		ID: 1006, RemainingWeight: 720, Price: 31.99, InitialWeight: 1000,
		Filament: demoSpoolFilament{Name: "Sunset Orange", Material: "TPU", Density: 1.21, Weight: 1000, Price: 31.99,
			Vendor: demoSpoolVendor{Name: "NinjaTek"}},
	},
}

// demoPrimarySpool returns the first non-archived demo spool of a material (case-insensitive, "pla" or
// "PLA"). The demo Filament list and every seeded quote price from it, so the demo has one filament story.
func demoPrimarySpool(material string) (demoSpool, bool) {
	for _, s := range demoSpools {
		if !s.Archived && strings.EqualFold(s.Filament.Material, material) {
			return s, true
		}
	}
	return demoSpool{}, false
}

// demoSpoolPricePerKg is spoolman.ts's spoolPricePerKg, rounded to cents like spoolOverrideFrom: the
// spool's own price over its initial weight.
func demoSpoolPricePerKg(s demoSpool) float64 {
	return math.Round(s.Price/s.InitialWeight*1000*100) / 100
}

// demoSpoolLabel is spoolman.ts's spoolLabel: "<vendor> <name> #<id>".
func demoSpoolLabel(s demoSpool) string {
	return fmt.Sprintf("%s %s #%d", s.Filament.Vendor.Name, s.Filament.Name, s.ID)
}

// demoSpoolmanHandler serves demoSpools as a bare JSON array, like Spoolman's GET /api/v1/spool.
func demoSpoolmanHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(demoSpools); err != nil {
		log.Printf("demo spoolman: encode: %v", err)
	}
}

// registerDemoSpoolman mounts the handler at SPOOLMAN_SPOOL_PATH, GET and HEAD only, gated on
// "filament" like the real proxy.
func registerDemoSpoolman(mux *http.ServeMux, db *sql.DB) {
	handler := requirePermission(db, "filament", demoSpoolmanHandler)
	mux.HandleFunc("GET /api/spoolman/api/v1/spool", handler)
	mux.HandleFunc("HEAD /api/spoolman/api/v1/spool", handler)
}

// demoMoonrakerJob mirrors the MoonrakerJob fields the app reads.
type demoMoonrakerJob struct {
	JobID         string  `json:"job_id"`
	Filename      string  `json:"filename"`
	Status        string  `json:"status"`
	FilamentUsed  float64 `json:"filament_used"`  // mm
	PrintDuration float64 `json:"print_duration"` // s
	TotalDuration float64 `json:"total_duration"` // s
	EndTime       int64   `json:"end_time"`       // unix seconds
}

// demoMoonrakerJobs is a handful of completed print jobs.
var demoMoonrakerJobs = []demoMoonrakerJob{
	{JobID: "000004", Filename: "enclosure_lid_0.2mm_ABS.gcode", Status: "completed", FilamentUsed: 21300, PrintDuration: 18600, TotalDuration: 19260, EndTime: 1745835600},
	{JobID: "000003", Filename: "vase_spiral_0.28mm_PLA.gcode", Status: "completed", FilamentUsed: 6100, PrintDuration: 5040, TotalDuration: 5220, EndTime: 1745752200},
	{JobID: "000002", Filename: "bracket_x4_0.2mm_PETG.gcode", Status: "completed", FilamentUsed: 15200, PrintDuration: 12480, TotalDuration: 12960, EndTime: 1745668500},
	{JobID: "000001", Filename: "benchy_0.2mm_PLA.gcode", Status: "completed", FilamentUsed: 8400, PrintDuration: 7380, TotalDuration: 7620, EndTime: 1745583900},
}

// demoMoonrakerHistoryHandler serves {"result":{"count":N,"jobs":[...]}}, like GET /server/history/list.
func demoMoonrakerHistoryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	resp := struct {
		Result struct {
			Count int                `json:"count"`
			Jobs  []demoMoonrakerJob `json:"jobs"`
		} `json:"result"`
	}{}
	resp.Result.Count = len(demoMoonrakerJobs)
	resp.Result.Jobs = demoMoonrakerJobs
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("demo moonraker history: encode: %v", err)
	}
}

// demoMoonrakerMetadataResult mirrors MoonrakerMetadata plus Filename, which the real response echoes back.
type demoMoonrakerMetadataResult struct {
	Filename            string  `json:"filename"`
	FilamentTotal       float64 `json:"filament_total"`        // mm
	FilamentWeightTotal float64 `json:"filament_weight_total"` // g
	EstimatedTime       float64 `json:"estimated_time"`        // s
}

// demoMoonrakerMetadataHandler serves plausible slicer metadata for any requested filename, wrapped in
// {"result":{...}}. Reuses filenameTraversalUnsafe so malformed filenames are rejected as on the real proxy.
func demoMoonrakerMetadataHandler(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filenameTraversalUnsafe(filename) {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	resp := struct {
		Result demoMoonrakerMetadataResult `json:"result"`
	}{Result: demoMoonrakerMetadataResult{
		Filename:            filename,
		FilamentTotal:       7800,
		FilamentWeightTotal: 62.5,
		EstimatedTime:       7200,
	}}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("demo moonraker metadata: encode: %v", err)
	}
}

// registerDemoMoonraker mounts fake history/metadata handlers (GET/HEAD), gated on "quote" like the proxy.
func registerDemoMoonraker(mux *http.ServeMux, db *sql.DB) {
	history := requirePermission(db, "quote", demoMoonrakerHistoryHandler)
	metadata := requirePermission(db, "quote", demoMoonrakerMetadataHandler)
	mux.HandleFunc("GET /api/moonraker/server/history/list", history)
	mux.HandleFunc("HEAD /api/moonraker/server/history/list", history)
	mux.HandleFunc("GET /api/moonraker/server/files/metadata", metadata)
	mux.HandleFunc("HEAD /api/moonraker/server/files/metadata", metadata)
}
