package docker

import "testing"

func TestNormalizeImageReference(t *testing.T) {
	tests := map[string]string{
		"nginx":                                      "nginx:latest",
		"redis:7":                                    "redis:7",
		"ghcr.io/acme/app:stable":                    "ghcr.io/acme/app:stable",
		"https://ghcr.io/acme/app:stable":            "ghcr.io/acme/app:stable",
		"https://hub.docker.com/_/redis":             "redis:latest",
		"https://hub.docker.com/r/library/nginx":     "library/nginx:latest",
		"registry.example.com/team/app@sha256:abc123": "registry.example.com/team/app@sha256:abc123",
	}
	for input, want := range tests {
		if got := normalizeImageReference(input); got != want {
			t.Fatalf("normalizeImageReference(%q) = %q, want %q", input, got, want)
		}
	}
}
