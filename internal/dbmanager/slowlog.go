package dbmanager

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	slowLogMaxEntries = 100
	slowLogMaxBytes = 4 * 1024 * 1024
)

type SlowQuery struct {
	Time       string
	DurationMS float64
	Detail     string
	Rows       string
}

type SlowLogInfo struct {
	Engine     string
	Enabled    bool
	Threshold  string
	ThresholdMS int
	Source     string
	Notice     string
}

func (m *Manager) SlowQueries(ctx context.Context, id int64) (SlowLogInfo, []SlowQuery, error) {
	db, err := m.Get(id)
	if err != nil {
		return SlowLogInfo{}, nil, err
	}
	if !databaseNameRE.MatchString(db.Name) {
		return SlowLogInfo{}, nil, errors.New("invalid managed database name")
	}
	switch db.Engine {
	case "mysql":
		return mysqlSlowQueries(ctx, db)
	case "postgres":
		return postgresSlowQueries(ctx, db)
	default:
		return SlowLogInfo{}, nil, errors.New("slow query logs are unavailable for this engine")
	}
}

// ConfigureSlowQueries updates logging explicitly on administrator request.
// MySQL slow-query logging is global; PostgreSQL duration logging is per database.
func (m *Manager) ConfigureSlowQueries(ctx context.Context, id int64, thresholdMS int) error {
	db, err := m.Get(id)
	if err != nil {
		return err
	}
	if !databaseNameRE.MatchString(db.Name) {
		return errors.New("invalid managed database name")
	}
	switch thresholdMS {
	case 0, 500, 1000, 2000, 5000, 10000:
	default:
		return errors.New("invalid slow-query threshold")
	}
	switch db.Engine {
	case "mysql":
		if thresholdMS == 0 {
			out, err := exec.CommandContext(ctx, "mysql", "--batch", "--skip-column-names",
				"--protocol=socket", "-uroot", "-e", "SET PERSIST slow_query_log = OFF;").CombinedOutput()
			if err != nil {
				return fmt.Errorf("disable MySQL slow log: %w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
		// SET PERSIST retains the change across MySQL restarts. Keep FILE output,
		// including existing log files, alongside TABLE for per-database filtering.
		sql := fmt.Sprintf("SET PERSIST log_output = 'FILE,TABLE'; SET PERSIST slow_query_log = ON; SET PERSIST long_query_time = %g;",
			float64(thresholdMS)/1000)
		out, err := exec.CommandContext(ctx, "mysql", "--batch", "--skip-column-names", "--protocol=socket", "-uroot", "-e", sql).CombinedOutput()
		if err != nil {
			return fmt.Errorf("enable MySQL slow log: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "postgres":
		var sql string
		if thresholdMS == 0 {
			sql = fmt.Sprintf("ALTER DATABASE \"%s\" RESET log_min_duration_statement;", db.Name)
		} else {
			sql = fmt.Sprintf("ALTER DATABASE \"%s\" SET log_min_duration_statement = '%dms';", db.Name, thresholdMS)
		}
		out, err := exec.CommandContext(ctx, "runuser", "-u", "postgres", "--",
			"psql", "-X", "-v", "ON_ERROR_STOP=1", "-d", "postgres", "-c", sql).CombinedOutput()
		if err != nil {
			return fmt.Errorf("configure PostgreSQL slow log: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	default:
		return errors.New("unsupported database engine")
	}
}

func mysqlSlowQueries(ctx context.Context, db Database) (SlowLogInfo, []SlowQuery, error) {
	info := SlowLogInfo{Engine: "mysql", Source: "mysql.slow_log"}
	out, err := exec.CommandContext(ctx, "mysql", "--batch", "--raw", "--skip-column-names",
		"--protocol=socket", "-uroot", "-e",
		"SELECT @@GLOBAL.slow_query_log, @@GLOBAL.long_query_time, @@GLOBAL.log_output;").CombinedOutput()
	if err != nil {
		return info, nil, fmt.Errorf("read MySQL slow-log settings: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) < 3 {
		return info, nil, errors.New("unexpected MySQL slow-log settings output")
	}
	info.Enabled = fields[0] == "1" || strings.EqualFold(fields[0], "ON")
	info.Threshold = fields[1] + " seconds (server-wide)"
	if secs, err := strconv.ParseFloat(fields[1], 64); err == nil { info.ThresholdMS = int(secs*1000 + 0.5) }
	if !info.Enabled || !strings.Contains(strings.ToUpper(fields[2]), "TABLE") {
		info.Notice = "Per-database query history requires MySQL slow_query_log=ON with TABLE output. " +
			"Enable it below to retain the existing FILE output and start collecting new entries."
		return info, nil, nil
	}
	// Database names originate from the validated, managed database catalog.
	// SQL text is transported as base64 to prevent tabs/newlines disrupting CLI rows.
	sql := fmt.Sprintf("SELECT DATE_FORMAT(start_time, '%%Y-%%m-%%d %%H:%%i:%%s'), "+
		"ROUND(TIME_TO_SEC(query_time)*1000, 3), rows_examined, "+
		"REPLACE(TO_BASE64(SUBSTRING(sql_text, 1, 4096)), CHAR(10), '') "+
		"FROM mysql.slow_log WHERE db = '%s' "+
		"ORDER BY start_time DESC LIMIT %d;", sqlString(db.Name), slowLogMaxEntries)
	out, err = exec.CommandContext(ctx, "mysql", "--batch", "--raw", "--skip-column-names",
		"--protocol=socket", "-uroot", "-e", sql).CombinedOutput()
	if err != nil {
		return info, nil, fmt.Errorf("read MySQL slow queries: %w: %s", err, strings.TrimSpace(string(out)))
	}
	queries := make([]SlowQuery, 0, slowLogMaxEntries)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		cols := strings.SplitN(line, "\t", 4)
		if len(cols) != 4 {
			continue
		}
		ms, err := strconv.ParseFloat(cols[1], 64)
		if err != nil {
			continue
		}
		rawSQL, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cols[3]))
		if err != nil {
			continue
		}
		queries = append(queries, SlowQuery{Time: cols[0], DurationMS: ms,
			Rows: cols[2], Detail: string(rawSQL)})
	}
	info.Notice = "MySQL slow-log settings apply to every MySQL database. Results are filtered to this database."
	return info, queries, nil
}

var postgresDurationRE = regexp.MustCompile(`(?i)\bLOG:\s+duration:\s+([0-9.]+)\s+ms\b`)

func postgresSlowQueries(ctx context.Context, db Database) (SlowLogInfo, []SlowQuery, error) {
	info := SlowLogInfo{Engine: "postgres", Source: "PostgreSQL server log"}
	out, err := exec.CommandContext(ctx, "runuser", "-u", "postgres", "--",
		"psql", "-X", "-At", "-d", db.Name, "-c",
		"SHOW log_min_duration_statement", "-c", "SHOW log_line_prefix").CombinedOutput()
	if err != nil {
		return info, nil, fmt.Errorf("read PostgreSQL logging settings: %w: %s", err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 2 {
		return info, nil, errors.New("unexpected PostgreSQL logging settings output")
	}
	info.Threshold = strings.TrimSpace(lines[0])
	if d, err := time.ParseDuration(info.Threshold); err == nil { info.ThresholdMS = int(d / time.Millisecond) }
	prefix := strings.TrimSpace(lines[1])
	info.Enabled = info.Threshold != "-1" && info.Threshold != "-1ms"
	if !info.Enabled {
		info.Notice = "Slow-query logging is disabled for this database. Enable it below; new connections will use the threshold."
		return info, nil, nil
	}
	if !strings.Contains(prefix, "%u@%d") {
		info.Notice = "PostgreSQL log_line_prefix does not include %u@%d (user and database); per-database filtering cannot be verified. "+
			"Add %u@%d to log_line_prefix in PostgreSQL configuration."
		return info, nil, nil
	}
	paths, err := filepath.Glob("/var/log/postgresql/postgresql-*-main.log")
	if err != nil || len(paths) == 0 {
		info.Notice = "No PostgreSQL file log found at /var/log/postgresql/postgresql-*-main.log. "+
			"Check the server's logging destination and log permissions."
		return info, nil, nil
	}
	sort.Slice(paths, func(i, j int) bool {
		a, aErr := os.Stat(paths[i])
		b, bErr := os.Stat(paths[j])
		if aErr != nil { return false }
		if bErr != nil { return true }
		return a.ModTime().After(b.ModTime())
	})
	var file *os.File
	for _, path := range paths {
		file, err = os.Open(path)
		if err == nil {
			break
		}
	}
	if file == nil {
		return info, nil, fmt.Errorf("open PostgreSQL server log: %w", err)
	}
	defer file.Close()
	info.Source = filepath.Base(file.Name())
	data, err := readLogTail(file, slowLogMaxBytes)
	if err != nil {
		return info, nil, err
	}
	queries := parsePostgresSlowQueries(string(data), db.Name, slowLogMaxEntries)
	info.Notice = "Only recent entries in the last 4 MiB of the server log are scanned. "+
		"Existing PostgreSQL sessions may need reconnecting after changing the threshold."
	return info, queries, nil
}

func readLogTail(file *os.File, maxBytes int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil { return nil, err }
	offset := info.Size() - maxBytes
	if offset < 0 { offset = 0 }
	if _, err := file.Seek(offset, io.SeekStart); err != nil { return nil, err }
	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil { return nil, err }
	if offset > 0 {
		if idx := strings.IndexByte(string(data), '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}
	return data, nil
}

func parsePostgresSlowQueries(log, database string, limit int) []SlowQuery {
	var queries []SlowQuery
	// PostgreSQL's Ubuntu/Debian default prefix is "%m [%p] %q%u@%d ".
	// Reject un-attributed messages rather than showing another database's SQL.
	databaseMarker := "@" + database + " "
	for _, line := range strings.Split(log, "\n") {
		loc := postgresDurationRE.FindStringSubmatchIndex(line)
		if loc == nil { continue }
		prefix := line[:loc[0]]
		if !strings.Contains(prefix, databaseMarker) {
			continue
		}
		ms, err := strconv.ParseFloat(line[loc[2]:loc[3]], 64)
		if err != nil { continue }
		timeText := ""
		if bracket := strings.Index(prefix, " ["); bracket > 0 {
			timeText = strings.TrimSpace(prefix[:bracket])
		}
		statement := strings.TrimSpace(line[loc[1]:])
		if len(statement) > 4096 { statement = statement[:4096] }
		queries = append(queries, SlowQuery{Time: timeText, DurationMS: ms, Detail: statement})
		if len(queries) > limit {
			queries = queries[1:]
		}
	}
	for i, j := 0, len(queries)-1; i < j; i, j = i+1, j-1 {
		queries[i], queries[j] = queries[j], queries[i]
	}
	return queries
}

