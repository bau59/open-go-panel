package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"sort"
	"strings"
	"time"
)

const performanceMaxRecords = 20000

type PerformanceFilter struct {
	AppID int64
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

type ServerTimingMetric struct {
	Name string
	DurationMS float64
}

type PerformancePoint struct {
	AppID int64
	RequestID string
	ServerTimings []ServerTimingMetric
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

var timingNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,39}$`)

// Only explicit backend-measured Server-Timing durations are accepted.
func parseServerTiming(values []string) []ServerTimingMetric {
	var metrics []ServerTimingMetric
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			parts := strings.Split(strings.TrimSpace(item), ";")
			if len(parts) < 2 { continue }
			name := strings.TrimSpace(parts[0])
			if !timingNameRE.MatchString(name) { continue }
			for _, param := range parts[1:] {
				key,raw,ok := strings.Cut(strings.TrimSpace(param), "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(key), "dur") { continue }
				raw = strings.Trim(strings.TrimSpace(raw), "\"")
				ms,err := strconv.ParseFloat(raw,64)
				if err != nil || ms < 0 || ms > 100000 || math.IsNaN(ms) || math.IsInf(ms,0){continue}
				metrics=append(metrics,ServerTimingMetric{Name:name,DurationMS:ms})
				break
			}
			if len(metrics)>=20{return metrics}
		}
	}
	return metrics
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
	if f.Since.IsZero() || f.Until.IsZero() || !f.Since.Before(f.Until) { return PerformanceResult{}, fmt.Errorf("invalid performance time window") }
	if f.Until.Sub(f.Since)>31*24*time.Hour { return PerformanceResult{}, fmt.Errorf("performance window cannot exceed 31 days") }
	if f.ThresholdMS<=0 { f.ThresholdMS=500 }
	if f.Page<1 { f.Page=1 }
	if f.PerPage<1||f.PerPage>200 { f.PerPage=50 }
	q:=`SELECT time_ns,app_id,domain,method,route,protocol,status,duration_ms,response_bytes,request_id,server_timings
	FROM http_perf_requests WHERE time_ns>=? AND time_ns<=?`
	args:=[]any{f.Since.UnixNano(),f.Until.UnixNano()}
	if f.Domain!="" {q+=" AND domain=?";args=append(args,f.Domain)}
	if f.AppID>0 {q+=" AND app_id=?";args=append(args,f.AppID)}
	if f.Method!="" {q+=" AND method=?";args=append(args,f.Method)}
	if f.Route!="" {q+=" AND instr(lower(route),lower(?))>0";args=append(args,f.Route)}
	if f.Status>0 {q+=" AND status BETWEEN ? AND ?";args=append(args,(f.Status/100)*100,(f.Status/100)*100+99)}
	if f.SlowOnly {q+=" AND duration_ms>=?";args=append(args,f.ThresholdMS)}
	q+=" ORDER BY time_ns DESC LIMIT ?"
	args=append(args,performanceMaxRecords+1)
	rows,err:=m.store.DB().QueryContext(ctx,q,args...)
	if err!=nil {return PerformanceResult{},err}
	var records []PerformancePoint
	for rows.Next(){
		var point PerformancePoint
		var nano int64
		var timings string
		if err=rows.Scan(&nano,&point.AppID,&point.Domain,&point.Method,&point.Route,&point.Protocol,&point.Status,
			&point.DurationMS,&point.Size,&point.RequestID,&timings);err!=nil{break}
		point.Time=time.Unix(0,nano).UTC()
		_ = json.Unmarshal([]byte(timings),&point.ServerTimings)
		records=append(records,point)
	}
	if err==nil {err=rows.Err()}
	_ = rows.Close()
	if err!=nil {return PerformanceResult{},err}
	result:=PerformanceResult{}
	if len(records)>performanceMaxRecords {records=records[:performanceMaxRecords];result.Truncated=true}
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

