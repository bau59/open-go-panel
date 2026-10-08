package caddy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const performanceMaxRecords = 20000

type PerformanceFilter struct {
	Since time.Time
	Until time.Time
	Domain string
	Method string
	Route string
	Status int
	SlowOnly bool
	ThresholdMS float64
	Page int
	PerPage int
	Sort string
}

type PerformancePoint struct {
	Time time.Time
	Domain string
	Method string
	Route string
	Protocol string
	Status int
	DurationMS float64
	Size int64
}

type PerformanceBucket struct {
	Time time.Time
	Count int
	P50, P95, P99 float64
}

type PerformanceRoute struct {
	Domain, Method, Route string
	Count, Errors int
	P50, P95, P99, Max float64
}

type PerformanceResult struct {
	Total int
	Slow int
	Errors int
	Average, P50, P95, P99 float64
	Rows []PerformancePoint
	Routes []PerformanceRoute
	Buckets []PerformanceBucket
	Truncated bool
	HasNext bool
}

var routeNumeric = regexp.MustCompile(`^[0-9]+$`)
var routeUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func cleanPerformanceRoute(value string) string {
	path := strings.SplitN(value, "?", 2)[0]
	if parsed, err := url.ParseRequestURI(value); err == nil {
		path = parsed.Path
	}
	if path == "" { return "/" }
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if routeNumeric.MatchString(part) || routeUUID.MatchString(part) {
			parts[i] = "{id}"
		}
	}
	result := strings.Join(parts, "/")
	if len(result) > 400 { result = result[:400] }
	return result
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 { return 0 }
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

func (m *Manager) QueryPerformance(ctx context.Context, f PerformanceFilter) (PerformanceResult, error) {
	if f.Since.IsZero() || f.Until.IsZero() || !f.Since.Before(f.Until) {
		return PerformanceResult{}, fmt.Errorf("invalid performance time window")
	}
	if f.Until.Sub(f.Since) > 31*24*time.Hour {
		return PerformanceResult{}, fmt.Errorf("performance window cannot exceed 31 days")
	}
	if f.ThresholdMS <= 0 { f.ThresholdMS = 500 }
	if f.Page < 1 { f.Page = 1 }
	if f.PerPage < 1 || f.PerPage > 200 { f.PerPage = 50 }
	cmd := exec.CommandContext(ctx, "journalctl", "-u", "caddy.service", "-o", "json",
		"--no-pager", "--since", f.Since.Format("2006-01-02 15:04:05"),
		"--until", f.Until.Format("2006-01-02 15:04:05"))
	out, err := cmd.StdoutPipe()
	if err != nil { return PerformanceResult{}, err }
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil { return PerformanceResult{}, err }

	result := PerformanceResult{}
	records := make([]PerformancePoint, 0, 1024)
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var env journalEnvelope
		if json.Unmarshal(scanner.Bytes(), &env) != nil { continue }
		entry, ok := parseCaddyLog(env.Message)
		if !ok || entry.Kind != "access" || entry.Time.Before(f.Since) || entry.Time.After(f.Until) { continue }
		route := cleanPerformanceRoute(entry.URI)
		if f.Domain != "" && !strings.EqualFold(entry.Domain, f.Domain) { continue }
		if f.Method != "" && !strings.EqualFold(entry.Method, f.Method) { continue }
		if f.Route != "" && !strings.Contains(strings.ToLower(route), strings.ToLower(f.Route)) { continue }
		if f.Status > 0 && entry.Status/100 != f.Status/100 { continue }
		if f.SlowOnly && entry.DurationMS < f.ThresholdMS { continue }
		if len(records) >= performanceMaxRecords { result.Truncated = true; break }
		records = append(records, PerformancePoint{
			Time: entry.Time, Domain: entry.Domain, Method: entry.Method,
			Route: route, Protocol: entry.Protocol, Status: entry.Status,
			DurationMS: entry.DurationMS, Size: entry.Size,
		})
	}
	scanErr := scanner.Err()
	// Close stdout before Wait to avoid hanging when the sample limit is reached.
	_ = out.Close()
	if result.Truncated && cmd.Process != nil { _ = cmd.Process.Kill() }
	waitErr := cmd.Wait()
	if scanErr != nil && !result.Truncated { return result, scanErr }
	if waitErr != nil && !result.Truncated && ctx.Err() == nil {
		return result, fmt.Errorf("read Caddy journal: %w: %s", waitErr, stderr.String())
	}
	if ctx.Err() != nil { return result, ctx.Err() }
	result.Total = len(records)
	durations := make([]float64, 0, len(records))
	type group struct {point PerformanceRoute; times []float64}
	groups := map[string]*group{}
	type bucket struct {time time.Time; times []float64}
	buckets := map[int64]*bucket{}
	var sum float64
	width := time.Minute
	if f.Until.Sub(f.Since) > 2*time.Hour { width = 15*time.Minute }
	if f.Until.Sub(f.Since) > 24*time.Hour { width = time.Hour }
	for _, p := range records {
		sum += p.DurationMS
		durations = append(durations, p.DurationMS)
		if p.DurationMS >= f.ThresholdMS { result.Slow++ }
		if p.Status >= 500 { result.Errors++ }
		key := p.Domain + "\x00" + p.Method + "\x00" + p.Route
		g := groups[key]
		if g == nil { g = &group{point:PerformanceRoute{Domain:p.Domain,Method:p.Method,Route:p.Route}}; groups[key] = g }
		g.point.Count++
		if p.Status >= 500 { g.point.Errors++ }
		if p.DurationMS > g.point.Max { g.point.Max = p.DurationMS }
		g.times = append(g.times, p.DurationMS)
		bucketStart := p.Time.Truncate(width)
		b := buckets[bucketStart.Unix()]
		if b == nil { b = &bucket{time:bucketStart}; buckets[bucketStart.Unix()] = b }
		b.times = append(b.times, p.DurationMS)
	}
	sort.Float64s(durations)
	if len(durations) > 0 {
		result.Average = sum / float64(len(durations))
		result.P50 = percentile(durations, .50)
		result.P95 = percentile(durations, .95)
		result.P99 = percentile(durations, .99)
	}
	for _, g := range groups {
		sort.Float64s(g.times)
		g.point.P50 = percentile(g.times, .50)
		g.point.P95 = percentile(g.times, .95)
		g.point.P99 = percentile(g.times, .99)
		result.Routes = append(result.Routes, g.point)
	}
	sort.Slice(result.Routes, func(i,j int) bool {
		if result.Routes[i].P95 == result.Routes[j].P95 { return result.Routes[i].Count > result.Routes[j].Count }
		return result.Routes[i].P95 > result.Routes[j].P95
	})
	for _, b := range buckets {
		sort.Float64s(b.times)
		result.Buckets = append(result.Buckets, PerformanceBucket{
			Time:b.time, Count:len(b.times), P50:percentile(b.times,.5),
			P95:percentile(b.times,.95), P99:percentile(b.times,.99)})
	}
	sort.Slice(result.Buckets,func(i,j int)bool{return result.Buckets[i].Time.Before(result.Buckets[j].Time)})
	sort.Slice(records,func(i,j int)bool{
		if f.Sort == "duration" { return records[i].DurationMS > records[j].DurationMS }
		return records[i].Time.After(records[j].Time)
	})
	start := (f.Page-1)*f.PerPage
	if start > len(records) { start = len(records) }
	end := start+f.PerPage
	if end > len(records) { end = len(records) }
	result.HasNext = end < len(records)
	result.Rows = records[start:end]
	return result,nil
}

