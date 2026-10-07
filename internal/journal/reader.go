package journal

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Query struct {
	Service   string
	Namespace string
	Since     string
	Until     string
	Search    string
	Page      int
	PerPage   int
}

type Result struct {
	Lines   []string
	Page    int
	PerPage int
	HasNext bool
}

func Read(ctx context.Context, q Query) (Result, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return Result{}, errors.New("journalctl is not installed")
	}
	if strings.TrimSpace(q.Service) == "" {
		return Result{}, errors.New("journal service is required")
	}
	if q.Page < 1 {
		q.Page = 1
	}
	switch q.PerPage {
	case 25, 50, 100, 200:
	default:
		q.PerPage = 100
	}

	need := q.Page*q.PerPage + 1
	args := []string{"--no-pager", "-o", "short-iso", "-r", "-u", q.Service, "-n", strconv.Itoa(need)}
	if strings.TrimSpace(q.Namespace) != "" {
		args = append([]string{"--namespace=" + q.Namespace}, args...)
	}
	if strings.TrimSpace(q.Since) != "" {
		args = append(args, "--since", q.Since)
	}
	if strings.TrimSpace(q.Until) != "" {
		args = append(args, "--until", q.Until)
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		args = append(args, "--grep", regexp.QuoteMeta(search), "--case-sensitive=false")
	}

	out, err := exec.CommandContext(ctx, "journalctl", args...).CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("journalctl: %w: %s", err, strings.TrimSpace(string(out)))
	}

	raw := strings.TrimSpace(string(out))
	var lines []string
	if raw != "" && raw != "-- No entries --" {
		lines = strings.Split(raw, "\n")
	}

	start := (q.Page - 1) * q.PerPage
	if start > len(lines) {
		start = len(lines)
	}
	end := start + q.PerPage
	if end > len(lines) {
		end = len(lines)
	}
	hasNext := len(lines) > q.Page*q.PerPage

	return Result{
		Lines:   append([]string(nil), lines[start:end]...),
		Page:    q.Page,
		PerPage: q.PerPage,
		HasNext: hasNext,
	}, nil
}
