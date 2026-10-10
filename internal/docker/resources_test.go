package docker

import (
	"strings"
	"testing"
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
