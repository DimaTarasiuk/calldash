package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte
loc, _ := time.LoadLocation("Europe/Kyiv")
now := time.Now().In(loc)

// ── Data structures ──────────────────────────────────────────────

type HourSlot struct {
	Hour     string `json:"hour"`
	Total    int    `json:"total"`
	Accepted int    `json:"accepted"`
	Agreed   int    `json:"agreed"`
	Callback int    `json:"callback"`
}

type DayRecord struct {
	Date  string     `json:"date"`
	Slots []HourSlot `json:"slots"`
}

type Store struct {
	Days []DayRecord `json:"days"`
}

// ── Global state ─────────────────────────────────────────────────

var (
	mu       sync.Mutex
	store    Store
	dataFile = "calls.json"
)

// ── Persistence ──────────────────────────────────────────────────

func loadStore() {
	data, err := os.ReadFile(dataFile)
	if err != nil {
		store = Store{}
		return
	}
	_ = json.Unmarshal(data, &store)
}

func saveStore() {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		log.Println("marshal error:", err)
		return
	}

	if err := os.WriteFile(dataFile, data, 0644); err != nil {
		log.Println("write error:", err)
	}
}

// ── Helpers ───────────────────────────────────────────────────────

func todayKey() string {
	return time.Now().In(loc).Format("2006-01-02")
}

func currentHourLabel() string {
	return fmt.Sprintf("%02d:00", time.Now().In(loc).Hour())
}

func getToday() *DayRecord {
	today := todayKey()

	for i := range store.Days {
		if store.Days[i].Date == today {
			return &store.Days[i]
		}
	}

	rec := DayRecord{
		Date:  today,
		Slots: make([]HourSlot, 24),
	}

	for h := 0; h < 24; h++ {
		rec.Slots[h] = HourSlot{
			Hour: fmt.Sprintf("%02d:00", h),
		}
	}

	store.Days = append(store.Days, rec)
	return &store.Days[len(store.Days)-1]
}

func getSlot(day *DayRecord, hourLabel string) *HourSlot {
	for i := range day.Slots {
		if day.Slots[i].Hour == hourLabel {
			return &day.Slots[i]
		}
	}
	return nil
}

// ── Handlers ─────────────────────────────────────────────────────

func handleCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	callType := r.URL.Query().Get("type")
	if callType == "" {
		callType = "total"
	}

	mu.Lock()
	defer mu.Unlock()

	day := getToday()
	slot := getSlot(day, currentHourLabel())
	if slot == nil {
		http.Error(w, "slot not found", 500)
		return
	}

	switch callType {
	case "total":
		slot.Total++
	case "accepted":
		slot.Total++
		slot.Accepted++
	case "agreed":
		slot.Total++
		slot.Accepted++
		slot.Agreed++
	case "callback":
		slot.Total++
		slot.Callback++
	}

	saveStore()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(slot)
}

func handleToday(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	day := getToday()
	curHour := currentHourLabel()

	var result []HourSlot
	for _, s := range day.Slots {
		if s.Hour <= curHour {
			result = append(result, s)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	type DaySummary struct {
		Date     string `json:"date"`
		Total    int    `json:"total"`
		Accepted int    `json:"accepted"`
		Agreed   int    `json:"agreed"`
		Callback int    `json:"callback"`
	}

	var out []DaySummary

	for _, d := range store.Days {
		var tot, acc, agr, cb int

		for _, s := range d.Slots {
			tot += s.Total
			acc += s.Accepted
			agr += s.Agreed
			cb += s.Callback
		}

		out = append(out, DaySummary{
			Date:     d.Date,
			Total:    tot,
			Accepted: acc,
			Agreed:   agr,
			Callback: cb,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleNow(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(loc)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"time": now.Format("15:04:05"),
		"hour": fmt.Sprintf("%02d:00", now.Hour()),
		"date": now.Format("2006-01-02"),
	})
}

// ── Static ───────────────────────────────────────────────────────

func handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// ── Main ─────────────────────────────────────────────────────────

func main() {
	log.Println("data file:", dataFile)

	loadStore()

	http.HandleFunc("/api/call", handleCall)
	http.HandleFunc("/api/today", handleToday)
	http.HandleFunc("/api/history", handleHistory)
	http.HandleFunc("/api/now", handleNow)
	http.HandleFunc("/", handleStatic)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	log.Println("Listening on", addr)

	log.Fatal(http.ListenAndServe(addr, nil))
}