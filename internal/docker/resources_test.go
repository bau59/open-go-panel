package docker

import (
	"strings"
	"testing"
	"context"
	"os"
	"path/filepath"
)

func TestParseLimitArgs(t *testing.T) {
	for _, tc := range []struct {
		name, cpu, memory string
		want []string
		fail bool
	}{
		{"unlimited", "0", "0", []string{"--cpus", "0", "--memory", "0"}, false},
		{"limited", "1.5", "512", []string{"--cpus", "1.5", "--memory", "536870912"}, false},
		{"small", "0.05", "6", []string{"--cpus", "0.05", "--memory", "6291456"}, false},
		{"missing cpu", "", "256", nil, true},
		{"negative cpu", "-1", "256", nil, true},
		{"invalid cpu", "1x", "256", nil, true},
		{"too small cpu", "0.001", "256", nil, true},
		{"too many cores", "257", "256", nil, true},
		{"exponent", "1e9", "256", nil, true},
		{"missing memory", "1", "", nil, true},
		{"negative memory", "1", "-1", nil, true},
		{"too small memory", "1", "5", nil, true},
		{"fraction memory", "1", "256.5", nil, true},
		{"too much memory", "1", "1048577", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLimitArgs(tc.cpu, tc.memory)
			if (err != nil) != tc.fail {
				t.Fatalf("parseLimitArgs returned %v, want fail=%v", err, tc.fail)
			}
			if !tc.fail && strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v; want %v", got, tc.want)
			}
		})
	}
}

func TestDockerLogsOutputIsBounded(t *testing.T) {
	var output tailOutput
	_, _ = output.Write([]byte("first\n"))
	_, _ = output.Write([]byte(strings.Repeat("a", maxDockerLogBytes)))
	text := output.String()
	if !strings.HasPrefix(text, "[Output truncated") {
		t.Fatal("truncation warning missing")
	}
	if strings.Contains(text, "first") || !strings.HasSuffix(text, "aaaa") {
		t.Fatal("log output did not keep the most recent bytes")
	}
	if len(output.buf) != maxDockerLogBytes {
		t.Fatalf("log buffer has %d bytes; max %d", len(output.buf), maxDockerLogBytes)
	}
}

func TestReplacementPreservesResourceCaps(t *testing.T) {
	spec := replacementSpec{
		Name: "sample", Running: true, NanoCPUs: 1500000000,
		Memory: 512 * 1048576, MemorySwap: 1024 * 1048576,
	}
	args, cleanup, err := spec.launchArgs("sample:latest")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	joined := strings.Join(args, " ")
	for _, value := range []string{
		"--cpus 1.5", "--memory 536870912", "--memory-swap 1073741824",
	} {
		if !strings.Contains(joined, value) {
			t.Fatalf("missing %q from replacement args: %s", value, joined)
		}
	}
}


func TestParseCPUCount(t *testing.T) {
	for _, tc := range []struct {
		raw string
		want int
		bad bool
	}{
		{"2\n", 2, false},
		{"128", 128, false},
		{"", 0, true},
		{"0", 0, true},
		{"not-a-number", 0, true},
	} {
		got, err := parseCPUCount(tc.raw)
		if (err != nil) != tc.bad || (!tc.bad && got != tc.want) {
			t.Errorf("parseCPUCount(%q) = %d, %v; want %d, bad %v", tc.raw, got, err, tc.want, tc.bad)
		}
	}
}

func TestValidateAvailableCPUs(t *testing.T) {
	for _, tc := range []struct {
		name, cpu string
		count int
		fail bool
	}{
		{"unlimited", "0", 2, false},
		{"half core", "0.5", 2, false},
		{"all cores", "2", 2, false},
		{"too many cores", "50", 2, true},
		{"fraction too many", "2.01", 2, true},
		{"daemon info missing", "1", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateAvailableCPUs(tc.cpu, tc.count); (err != nil) != tc.fail {
				t.Fatalf("validateAvailableCPUs(%q,%d): err=%v want fail %v", tc.cpu, tc.count, err, tc.fail)
			}
		})
	}
}

func TestUpdateLimitsRejectsTooManyCPUsBeforeDockerUpdate(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "updated")
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"  info) echo 2 ;;\n" +
		"  update) printf '%s\\n' \"$*\" >> " + marker + " ;;\n" +
		"  *) exit 2 ;;\nesac\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	mgr := New()
	err := mgr.UpdateLimits(context.Background(), "abc123", "50", "500")
	if err == nil || !strings.Contains(err.Error(), "2 available cores") {
		t.Fatalf("expected informative host CPU capacity error; got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("docker update was attempted for an invalid CPU cap: %v", err)
	}
	if err := mgr.UpdateLimits(context.Background(), "abc123", "0.5", "500"); err != nil {
		t.Fatalf("valid Docker update rejected: %v", err)
	}
	out, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); !strings.Contains(got, "update --cpus 0.5 --memory 524288000 abc123") {
		t.Fatalf("unexpected docker update args: %q", got)
	}
}
