package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

var loc, _ = time.LoadLocation("Europe/Kyiv")

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

type GeoIPInfo struct {
	City        string `json:"city"`
	CountryName string `json:"country_name"`
	CountryCode string `json:"country_code"`
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

	// Створюємо новий день
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

func getDayByDate(date string) *DayRecord {
	for i := range store.Days {
		if store.Days[i].Date == date {
			return &store.Days[i]
		}
	}
	return nil
}

func createNewDay(date string) *DayRecord {
	rec := DayRecord{
		Date:  date,
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
		//slot.Accepted++
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

func handleSlotEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", 405)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	date := r.URL.Query().Get("date")
	hour := r.URL.Query().Get("hour")

	if date == "" || hour == "" {
		http.Error(w, "date and hour required", 400)
		return
	}

	day := getDayByDate(date)
	if day == nil {
		day = createNewDay(date)
	}

	slot := getSlot(day, hour)
	if slot == nil {
		http.Error(w, "slot not found", 404)
		return
	}

	var payload HourSlot
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	slot.Total = payload.Total
	slot.Accepted = payload.Accepted
	slot.Agreed = payload.Agreed
	slot.Callback = payload.Callback

	saveStore()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(slot)
}

func handleDayEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", 405)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	date := r.URL.Query().Get("date")
	if date == "" {
		http.Error(w, "date required", 400)
		return
	}

	day := getDayByDate(date)
	if day == nil {
		day = createNewDay(date)
	}

	var payload struct {
		Total    int `json:"total"`
		Accepted int `json:"accepted"`
		Agreed   int `json:"agreed"`
		Callback int `json:"callback"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	if len(day.Slots) > 0 {
		firstSlot := &day.Slots[0]
		firstSlot.Total = payload.Total
		firstSlot.Accepted = payload.Accepted
		firstSlot.Agreed = payload.Agreed
		firstSlot.Callback = payload.Callback
	}

	saveStore()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(day)
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

func handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ip := r.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = r.RemoteAddr
		}
		if idx := strings.LastIndex(ip, ":"); idx != -1 {
			ip = ip[:idx]
		}

		cityInfo := getCityFromIP(ip)

		next.ServeHTTP(w, r)

		duration := time.Since(start)
		log.Printf("[%s] %s %s | IP: %s | Location: %s, %s | Duration: %v",
			start.Format("2006-01-02 15:04:05"),
			r.Method,
			r.URL.Path,
			ip,
			cityInfo.City,
			cityInfo.CountryName,
			duration,
		)
	}
}

//IP
func getCityFromIP(ip string) GeoIPInfo {
	if ip == "" || ip == "127.0.0.1" || ip == "::1" {
		return GeoIPInfo{City: "Local", CountryName: "Local"}
	}

	url := fmt.Sprintf("https://ipapi.co/%s/json/", ip)

	resp, err := http.Get(url)
	if err != nil {
		return GeoIPInfo{City: "Unknown", CountryName: ""}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return GeoIPInfo{City: "Unknown", CountryName: ""}
	}

	body, _ := io.ReadAll(resp.Body)
	var info GeoIPInfo
	json.Unmarshal(body, &info)

	if info.City == "" {
		info.City = "Unknown"
	}
	return info
}

// ── Main ─────────────────────────────────────────────────────────

func main() {
	log.Println("Data file:", dataFile)
	loadStore()

	http.HandleFunc("/api/call", loggingMiddleware(handleCall))
	http.HandleFunc("/api/today", loggingMiddleware(handleToday))
	http.HandleFunc("/api/history", loggingMiddleware(handleHistory))
	http.HandleFunc("/api/slot", loggingMiddleware(handleSlotEdit))
	http.HandleFunc("/api/day", loggingMiddleware(handleDayEdit))
	http.HandleFunc("/api/now", loggingMiddleware(handleNow))
	http.HandleFunc("/", loggingMiddleware(handleStatic))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	log.Println("Server started on", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}